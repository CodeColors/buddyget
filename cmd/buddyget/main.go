// Commande buddyget : serveur web de gestion du budget du foyer.
//
// Usage :
//
//	buddyget              démarre le serveur
//	buddyget healthcheck  interroge /healthz (HEALTHCHECK Docker, image sans shell)
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
	_ "time/tzdata" // base des fuseaux embarquée : l'image scratch n'en a pas (TZ)

	// Certificats racines embarqués, utilisés faute de magasin système (image
	// scratch) : notifications SMTP chiffrées, webhook et copie WebDAV en
	// HTTPS.
	_ "golang.org/x/crypto/x509roots/fallback"

	"github.com/codecolors/buddyget/internal/alerts"
	"github.com/codecolors/buddyget/internal/attachments"
	"github.com/codecolors/buddyget/internal/auth"
	"github.com/codecolors/buddyget/internal/backup"
	"github.com/codecolors/buddyget/internal/budget"
	"github.com/codecolors/buddyget/internal/categorize"
	"github.com/codecolors/buddyget/internal/config"
	"github.com/codecolors/buddyget/internal/exports"
	"github.com/codecolors/buddyget/internal/forecast"
	"github.com/codecolors/buddyget/internal/goals"
	"github.com/codecolors/buddyget/internal/household"
	"github.com/codecolors/buddyget/internal/importer"
	"github.com/codecolors/buddyget/internal/ledger"
	"github.com/codecolors/buddyget/internal/loans"
	"github.com/codecolors/buddyget/internal/platform/calendar"
	"github.com/codecolors/buddyget/internal/platform/db"
	"github.com/codecolors/buddyget/internal/recurring"
	"github.com/codecolors/buddyget/internal/reports"
	"github.com/codecolors/buddyget/internal/savings"
	"github.com/codecolors/buddyget/internal/scenarios"
	"github.com/codecolors/buddyget/internal/scheduler"
	"github.com/codecolors/buddyget/internal/web"
)

// version est renseignée à la compilation (-ldflags "-X main.version=…").
var version = "dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		if err := healthcheck(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if err := run(); err != nil {
		slog.Error("arrêt sur erreur", "err", err)
		os.Exit(1)
	}
}

func run() error {
	start := time.Now()
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := os.MkdirAll(cfg.DataDir, 0o750); err != nil {
		return fmt.Errorf("dossier de données : %w", err)
	}
	// Une restauration préparée avant le redémarrage s'applique avant
	// l'ouverture de la base.
	if restored, err := backup.ApplyPending(cfg.DataDir, time.Now()); err != nil {
		return fmt.Errorf("restauration de la sauvegarde : %w", err)
	} else if restored {
		slog.Info("sauvegarde restaurée ; les données remplacées sont dans avant-restauration")
	}
	database, err := db.Open(ctx, cfg.DBPath())
	if err != nil {
		return err
	}
	defer func() {
		if err := database.Close(); err != nil {
			slog.Error("fermeture de la base", "err", err)
		}
	}()

	accounts, rules, recurrences := ledger.NewService(database), categorize.NewService(database), recurring.NewService(database)
	savingsAccounts, budgets := savings.NewService(database, accounts), budget.NewService(database, accounts)
	credits := loans.NewService(database, recurrences)
	whatIfs := scenarios.NewService(database, accounts, recurrences, credits)
	forecasts := forecast.NewService(database, forecast.Deps{
		Ledger: accounts, Recurring: recurrences, Savings: savingsAccounts, Budgets: budgets, Loans: credits, Scenarios: whatIfs,
	})
	baseURL := ""
	if cfg.BaseURL != nil {
		baseURL = cfg.BaseURL.String()
	}
	alertCenter := alerts.NewService(database, accounts, recurrences, savingsAccounts, budgets, forecasts, baseURL)
	files := attachments.NewService(database, cfg.DataDir, cfg.MaxUploadBytes(), cfg.AttachmentsQuotaBytes())
	backups := backup.NewService(database, cfg.DataDir, cfg.BackupPassphrase)
	handler, err := web.New(cfg, database, web.Services{
		Auth:        auth.NewService(database),
		Ledger:      accounts,
		Rules:       rules,
		Importer:    importer.NewService(database, cfg.DataDir, cfg.MaxUploadBytes(), rules),
		Recurring:   recurrences,
		Savings:     savingsAccounts,
		Budgets:     budgets,
		Forecast:    forecasts,
		Alerts:      alertCenter,
		Reports:     reports.NewService(database, accounts),
		Attachments: files,
		Loans:       credits,
		Goals:       goals.NewService(database, accounts, recurrences),
		Scenarios:   whatIfs,
		Household:   household.NewService(database, accounts),
		Exports:     exports.NewService(accounts, files),
		Backups:     backups,
		// Après une restauration préparée : arrêt propre, la politique de
		// redémarrage du conteneur relance BuddyGet.
		Restart: stop,
	})
	if err != nil {
		return err
	}
	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    16 << 10,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
	}
	ln, err := new(net.ListenConfig).Listen(ctx, "tcp", cfg.Addr())
	if err != nil {
		return err
	}
	slog.Info("BuddyGet démarré", "version", version, "adresse", cfg.Addr(), "fuseau", time.Local.String(),
		"démarrage", time.Since(start).Round(time.Millisecond))

	// Travaux de fond : examen des alertes à la demande, travaux quotidiens
	// (intérêts, puis alertes). Arrêtés, et attendus, avant la fermeture de
	// la base.
	var background sync.WaitGroup
	background.Go(func() { alertCenter.Run(ctx) })
	background.Go(func() {
		scheduler.Run(ctx,
			scheduler.Job{Name: "intérêts des années closes", Run: func(ctx context.Context, today calendar.Date) error {
				n, err := savingsAccounts.CloseYears(ctx, today)
				if n > 0 {
					slog.Info("intérêts inscrits", "écritures", n)
				}
				return err
			}},
			scheduler.Job{Name: "alertes", Run: alertCenter.Daily},
			scheduler.Job{Name: "pièces jointes orphelines", Run: func(ctx context.Context, _ calendar.Date) error {
				n, err := files.Sweep(ctx)
				if n > 0 {
					slog.Info("pièces jointes orphelines supprimées", "fichiers", n)
				}
				return err
			}},
			scheduler.Job{Name: "sauvegarde automatique", Run: func(ctx context.Context, _ calendar.Date) error {
				return backups.Daily(ctx, time.Now())
			}},
		)
	})
	defer func() {
		stop()
		background.Wait()
	}()

	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}

	slog.Info("arrêt demandé")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// healthcheck renvoie une erreur si le serveur local ne répond pas « ok ».
func healthcheck() error {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1"+cfg.Addr()+"/healthz", nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("healthz : statut %d", resp.StatusCode)
	}
	return nil
}
