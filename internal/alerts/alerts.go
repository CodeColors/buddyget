// Package alerts est le système d'alertes unique : il examine la situation
// (enveloppes, prévisionnel, opérations, abonnements, échéances, imports,
// épargne), crée une alerte par situation nouvelle, les présente jusqu'à
// leur acquittement et les relaie, si l'utilisateur l'a activé, par e-mail
// ou webhook. L'examen a lieu après une saisie ou un import et une fois par
// jour, jamais par interrogation périodique.
package alerts

import (
	"context"
	"database/sql"
	"slices"
	"sync"

	"github.com/codecolors/buddyget/internal/budget"
	"github.com/codecolors/buddyget/internal/forecast"
	"github.com/codecolors/buddyget/internal/ledger"
	"github.com/codecolors/buddyget/internal/platform/calendar"
	"github.com/codecolors/buddyget/internal/platform/db"
	"github.com/codecolors/buddyget/internal/platform/validation"
	"github.com/codecolors/buddyget/internal/recurring"
	"github.com/codecolors/buddyget/internal/savings"
)

// Kind est un type d'alerte.
type Kind string

const (
	BudgetKind   Kind = "budget"       // enveloppe presque épuisée ou dépassée
	LowBalance   Kind = "prevision"    // solde prévisionnel sous le seuil
	Unusual      Kind = "inhabituelle" // dépense très supérieure à l'habitude
	Subscription Kind = "abonnement"   // montant ou date d'une récurrence modifiés
	DueSoon      Kind = "echeance"     // échéance proche
	ImportStale  Kind = "import"       // pas d'import depuis longtemps
	Unlock       Kind = "deblocage"    // déblocage de fonds d'épargne imminent
)

// Kinds liste les types dans l'ordre d'affichage.
var Kinds = []Kind{BudgetKind, LowBalance, Unusual, Subscription, DueSoon, ImportStale, Unlock}

// Label décrit le type d'alerte.
func (k Kind) Label() string {
	switch k {
	case BudgetKind:
		return "Enveloppe presque épuisée ou dépassée"
	case LowBalance:
		return "Solde prévisionnel sous le seuil"
	case Unusual:
		return "Dépense inhabituelle"
	case Subscription:
		return "Abonnement dont le montant ou la date change"
	case DueSoon:
		return "Échéance proche"
	case ImportStale:
		return "Pas d'import récent"
	}
	return "Déblocage de fonds d'épargne"
}

// ParamLabel décrit le paramètre du type, et paramBounds ses bornes.
func (k Kind) ParamLabel() string {
	switch k {
	case BudgetKind:
		return "Alerter à partir de (% consommé)"
	case LowBalance:
		return "Horizon examiné (jours)"
	case Unusual:
		return "Montant supérieur à l'habitude de (fois)"
	case Subscription:
		return "Écart de montant toléré (%)"
	case DueSoon:
		return "Prévenir (jours avant)"
	case ImportStale:
		return "Après (jours sans import)"
	}
	return "Prévenir (jours avant)"
}

func (k Kind) paramBounds() (int, int) {
	switch k {
	case BudgetKind:
		return 1, 100
	case Unusual:
		return 2, 20
	case Subscription:
		return 1, 100
	case LowBalance:
		return 1, 730
	}
	return 1, 365
}

// Setting est le réglage d'un type d'alerte.
type Setting struct {
	Kind    Kind
	Enabled bool
	Param   int
}

// Alert est une alerte présentée dans l'application.
type Alert struct {
	ID           int64
	Kind         Kind
	Key          string // situation signalée : une seule alerte par clé
	Title        string
	Detail       string
	Link         string
	CreatedOn    calendar.Date
	Acknowledged bool
}

// Service examine la situation et gère les alertes.
type Service struct {
	db        *db.DB
	ledger    *ledger.Service
	recurring *recurring.Service
	savings   *savings.Service
	budgets   *budget.Service
	forecast  *forecast.Service
	baseURL   string // adresse publique, pour les liens des notifications
	transport transport
	trigger   chan struct{}
	mu        sync.Mutex // un examen à la fois
}

// NewService crée le service des alertes ; baseURL (facultative) sert aux
// liens des notifications.
func NewService(d *db.DB, l *ledger.Service, r *recurring.Service, sv *savings.Service, b *budget.Service, f *forecast.Service, baseURL string) *Service {
	return &Service{
		db: d, ledger: l, recurring: r, savings: sv, budgets: b, forecast: f, baseURL: baseURL,
		transport: netTransport{}, trigger: make(chan struct{}, 1),
	}
}

// Settings renvoie le réglage de chaque type, dans l'ordre d'affichage.
func (s *Service) Settings(ctx context.Context) ([]Setting, error) {
	rows, err := s.db.R.QueryContext(ctx, `SELECT kind, enabled, param FROM alert_settings`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Setting
	for rows.Next() {
		var st Setting
		if err := rows.Scan(&st.Kind, &st.Enabled, &st.Param); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	slices.SortFunc(out, func(a, b Setting) int { return slices.Index(Kinds, a.Kind) - slices.Index(Kinds, b.Kind) })
	return out, rows.Err()
}

// Setting renvoie le réglage d'un type.
func (s *Service) Setting(ctx context.Context, k Kind) (Setting, error) {
	st := Setting{Kind: k}
	err := s.db.R.QueryRowContext(ctx, `SELECT enabled, param FROM alert_settings WHERE kind = ?`, k).Scan(&st.Enabled, &st.Param)
	return st, err
}

// SaveSettings enregistre les réglages des types d'alerte.
func (s *Service) SaveSettings(ctx context.Context, settings []Setting) error {
	for _, st := range settings {
		if !slices.Contains(Kinds, st.Kind) {
			return validation.Error("Type d'alerte inconnu.")
		}
		if lo, hi := st.Kind.paramBounds(); st.Param < lo || st.Param > hi {
			return validation.Errorf("%s : valeur entre %d et %d.", st.Kind.Label(), lo, hi)
		}
	}
	return s.db.Tx(ctx, func(tx *sql.Tx) error {
		for _, st := range settings {
			if _, err := tx.ExecContext(ctx, `UPDATE alert_settings SET enabled = ?, param = ? WHERE kind = ?`, st.Enabled, st.Param, st.Kind); err != nil {
				return err
			}
		}
		return nil
	})
}

const alertSelect = `SELECT id, kind, key, title, detail, link, created_on, acknowledged FROM alerts`

func (s *Service) list(ctx context.Context, query string, args ...any) ([]Alert, error) {
	rows, err := s.db.R.QueryContext(ctx, alertSelect+query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Alert
	for rows.Next() {
		var a Alert
		if err := rows.Scan(&a.ID, &a.Kind, &a.Key, &a.Title, &a.Detail, &a.Link, &a.CreatedOn, &a.Acknowledged); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// Open renvoie les alertes non acquittées, les plus récentes d'abord.
func (s *Service) Open(ctx context.Context) ([]Alert, error) {
	return s.list(ctx, ` WHERE NOT acknowledged ORDER BY id DESC`)
}

// Acknowledged renvoie les dernières alertes acquittées.
func (s *Service) Acknowledged(ctx context.Context, limit int) ([]Alert, error) {
	return s.list(ctx, ` WHERE acknowledged ORDER BY id DESC LIMIT ?`, limit)
}

// OpenCount compte les alertes non acquittées (cloche de l'en-tête).
func (s *Service) OpenCount(ctx context.Context) (int, error) {
	var n int
	err := s.db.R.QueryRowContext(ctx, `SELECT count(*) FROM alerts WHERE NOT acknowledged`).Scan(&n)
	return n, err
}

// Acknowledge acquitte une alerte ; id nul : toutes.
func (s *Service) Acknowledge(ctx context.Context, id int64) error {
	_, err := s.db.W.ExecContext(ctx, `UPDATE alerts SET acknowledged = 1 WHERE NOT acknowledged AND (? = 0 OR id = ?)`, id, id)
	return err
}

// keepDays est la durée de conservation des alertes acquittées.
const keepDays = 90

// record enregistre les alertes nouvelles (une situation déjà signalée,
// même acquittée, ne l'est pas de nouveau) et purge les alertes acquittées
// anciennes. Renvoie le nombre d'alertes créées.
func (s *Service) record(ctx context.Context, found []Alert, today calendar.Date) (int, error) {
	created := 0
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM alerts WHERE acknowledged AND created_on < ?`, today.AddDays(-keepDays)); err != nil {
			return err
		}
		for _, a := range found {
			res, err := tx.ExecContext(ctx, `
				INSERT INTO alerts (kind, key, title, detail, link, created_on) VALUES (?, ?, ?, ?, ?, ?)
				ON CONFLICT (key) DO NOTHING`, a.Kind, a.Key, a.Title, a.Detail, a.Link, today)
			if err != nil {
				return err
			}
			n, err := res.RowsAffected()
			if err != nil {
				return err
			}
			created += int(n)
		}
		return nil
	})
	return created, err
}
