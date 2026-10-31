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
	"syscall"
	"time"
	_ "time/tzdata" // base des fuseaux embarquée : l'image scratch n'en a pas (TZ)

	"github.com/codecolors/buddyget/internal/auth"
	"github.com/codecolors/buddyget/internal/config"
	"github.com/codecolors/buddyget/internal/platform/db"
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
	database, err := db.Open(ctx, cfg.DBPath())
	if err != nil {
		return err
	}
	defer func() {
		if err := database.Close(); err != nil {
			slog.Error("fermeture de la base", "err", err)
		}
	}()

	handler, err := web.New(cfg, database, auth.NewService(database))
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
	slog.Info("BuddyGet démarré", "version", version, "adresse", cfg.Addr(), "démarrage", time.Since(start).Round(time.Millisecond))

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
