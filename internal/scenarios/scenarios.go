// Package scenarios gère les scénarios « et si » : des calques nommés de
// modifications que le prévisionnel applique sans toucher aux données
// réelles (moteur unique), puis que l'on peut convertir en données réelles.
package scenarios

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/codecolors/buddyget/internal/ledger"
	"github.com/codecolors/buddyget/internal/loans"
	"github.com/codecolors/buddyget/internal/platform/calendar"
	"github.com/codecolors/buddyget/internal/platform/db"
	"github.com/codecolors/buddyget/internal/platform/money"
	"github.com/codecolors/buddyget/internal/platform/validation"
	"github.com/codecolors/buddyget/internal/recurring"
)

// ErrNotFound signale un scénario ou une modification inexistants.
var ErrNotFound = errors.New("scénario introuvable")

const (
	nameMaxLen  = 60
	labelMaxLen = 200
	// maxPercent borne une variation en pourcentage (centièmes de point).
	maxPercent = 100_000
)

// Kind est la nature d'une modification.
type Kind string

const (
	OneOff    Kind = "ponctuel"   // opération ponctuelle ajoutée
	Repeating Kind = "recurrent"  // opération récurrente ajoutée
	Suspend   Kind = "suspension" // récurrence arrêtée à une date
	Vary      Kind = "variation"  // récurrence en hausse ou en baisse à une date
	NewLoan   Kind = "credit"     // nouveau crédit
	Prepay    Kind = "anticipe"   // remboursement anticipé d'un crédit suivi
)

// Kinds sont les natures proposées, dans l'ordre d'affichage.
var Kinds = []Kind{OneOff, Repeating, Suspend, Vary, NewLoan, Prepay}

// Label décrit la nature d'une modification.
func (k Kind) Label() string {
	switch k {
	case OneOff:
		return "Opération ponctuelle"
	case Repeating:
		return "Opération récurrente"
	case Suspend:
		return "Arrêt d'une récurrence"
	case Vary:
		return "Hausse ou baisse d'une récurrence"
	case NewLoan:
		return "Nouveau crédit"
	case Prepay:
		return "Remboursement anticipé"
	}
	return string(k)
}

// Item est une modification d'un scénario. Les champs utiles dépendent de
// sa nature :
//   - ponctuel : AccountID, Date, Label, Amount signé ;
//   - recurrent : AccountID, Date (première échéance), End (facultative),
//     Months entre deux échéances, Label, Amount signé ;
//   - suspension : RuleID, Date (premières échéances supprimées) ;
//   - variation : RuleID, Date, Percent (centièmes de point) ou Amount,
//     écart sur la valeur absolue : positif, la dépense ou le revenu
//     augmente ;
//   - credit : AccountID, Date (première échéance), Label (nom), Amount
//     (capital), Rate, Months (durée), Insurance ;
//   - anticipe : LoanID, Date, Amount remboursé.
type Item struct {
	ID          int64
	Kind        Kind
	Label       string
	AccountID   int64
	AccountName string
	RuleID      int64
	RuleLabel   string
	LoanID      int64
	LoanName    string
	Amount      int64
	Percent     int
	Rate        int
	Months      int
	Insurance   int64
	Date        calendar.Date
	End         calendar.Date
}

// Title résume la modification.
func (it Item) Title() string {
	switch it.Kind {
	case Suspend:
		return "Arrêt : " + it.RuleLabel
	case Vary:
		return it.RuleLabel
	case NewLoan:
		return "Crédit " + it.Label
	case Prepay:
		return "Remboursement anticipé : " + it.LoanName
	}
	return it.Label
}

// Loan renvoie les caractéristiques d'un nouveau crédit.
func (it Item) Loan() loans.Input {
	return loans.Input{Name: it.Label, AccountID: it.AccountID, Params: loans.Params{
		Principal: it.Amount, Rate: it.Rate, Months: it.Months, Insurance: it.Insurance, First: it.Date,
	}}
}

// Period renvoie la périodicité d'une opération récurrente ajoutée.
func (it Item) Period() calendar.Period {
	return calendar.Period{Count: it.Months, Unit: calendar.Month}
}

// Scenario est un scénario et ses modifications, par date.
type Scenario struct {
	ID    int64
	Name  string
	Items []Item
}

// Service gère les scénarios.
type Service struct {
	db        *db.DB
	ledger    *ledger.Service
	recurring *recurring.Service
	loans     *loans.Service
}

// NewService crée le service des scénarios.
func NewService(d *db.DB, l *ledger.Service, r *recurring.Service, lo *loans.Service) *Service {
	return &Service{db: d, ledger: l, recurring: r, loans: lo}
}

// Scenarios renvoie les scénarios, par nom, avec leurs modifications.
func (s *Service) Scenarios(ctx context.Context) ([]Scenario, error) {
	out, err := db.Collect(ctx, s.db.R, func(row db.Scanner) (Scenario, error) {
		var sc Scenario
		err := row.Scan(&sc.ID, &sc.Name)
		return sc, err
	}, `SELECT id, name FROM scenarios ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	for i := range out {
		if out[i].Items, err = s.items(ctx, out[i].ID); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// Scenario renvoie un scénario.
func (s *Service) Scenario(ctx context.Context, id int64) (Scenario, error) {
	sc := Scenario{ID: id}
	err := s.db.R.QueryRowContext(ctx, `SELECT name FROM scenarios WHERE id = ?`, id).Scan(&sc.Name)
	if errors.Is(err, sql.ErrNoRows) {
		return sc, ErrNotFound
	}
	if err != nil {
		return sc, err
	}
	sc.Items, err = s.items(ctx, id)
	return sc, err
}

func (s *Service) items(ctx context.Context, scenarioID int64) ([]Item, error) {
	return db.Collect(ctx, s.db.R, func(row db.Scanner) (Item, error) {
		var it Item
		err := row.Scan(&it.ID, &it.Kind, &it.Label, &it.AccountID, &it.AccountName, &it.RuleID, &it.RuleLabel, &it.LoanID, &it.LoanName,
			&it.Amount, &it.Percent, &it.Rate, &it.Months, &it.Insurance, &it.Date, &it.End)
		return it, err
	}, `
		SELECT i.id, i.kind, i.label, coalesce(i.account_id, 0), coalesce(a.name, ''), coalesce(i.rule_id, 0), coalesce(r.label, ''),
			coalesce(i.loan_id, 0), coalesce(l.name, ''), i.amount, i.percent, i.rate, i.months, i.insurance, i.date, coalesce(i.end_date, '')
		FROM scenario_items i
		LEFT JOIN accounts a ON a.id = i.account_id
		LEFT JOIN recurring_rules r ON r.id = i.rule_id
		LEFT JOIN loans l ON l.id = i.loan_id
		WHERE i.scenario_id = ? ORDER BY i.date, i.id`, scenarioID)
}

// Create enregistre un scénario vide.
func (s *Service) Create(ctx context.Context, name string) (int64, error) {
	name, err := validName(name)
	if err != nil {
		return 0, err
	}
	res, err := s.db.W.ExecContext(ctx, `INSERT INTO scenarios (name) VALUES (?)`, name)
	if db.IsUniqueViolation(err) {
		return 0, validation.Error("Un scénario porte déjà ce nom.")
	}
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// Rename renomme un scénario.
func (s *Service) Rename(ctx context.Context, id int64, name string) error {
	name, err := validName(name)
	if err != nil {
		return err
	}
	res, err := s.db.W.ExecContext(ctx, `UPDATE scenarios SET name = ? WHERE id = ?`, name, id)
	if db.IsUniqueViolation(err) {
		return validation.Error("Un scénario porte déjà ce nom.")
	}
	return affectedOne(res, err)
}

// Delete supprime un scénario et ses modifications.
func (s *Service) Delete(ctx context.Context, id int64) error {
	res, err := s.db.W.ExecContext(ctx, `DELETE FROM scenarios WHERE id = ?`, id)
	return affectedOne(res, err)
}

// AddItem ajoute une modification à un scénario.
func (s *Service) AddItem(ctx context.Context, scenarioID int64, it Item) (int64, error) {
	it, err := validItem(it)
	if err != nil {
		return 0, err
	}
	res, err := s.db.W.ExecContext(ctx, `
		INSERT INTO scenario_items (scenario_id, kind, label, account_id, rule_id, loan_id, amount, percent, rate, months, insurance, date, end_date)
		VALUES (?, ?, ?, nullif(?, 0), nullif(?, 0), nullif(?, 0), ?, ?, ?, ?, ?, ?, nullif(?, ''))`,
		scenarioID, it.Kind, it.Label, it.AccountID, it.RuleID, it.LoanID, it.Amount, it.Percent, it.Rate, it.Months, it.Insurance, it.Date, it.End)
	if db.IsForeignKeyViolation(err) {
		return 0, s.missing(ctx, scenarioID, it)
	}
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// missing explique une référence introuvable : le scénario, sinon l'objet
// de la modification.
func (s *Service) missing(ctx context.Context, scenarioID int64, it Item) error {
	var ok bool
	if err := s.db.R.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM scenarios WHERE id = ?)`, scenarioID).Scan(&ok); err != nil {
		return err
	}
	switch {
	case !ok:
		return ErrNotFound
	case it.Kind == Suspend || it.Kind == Vary:
		return validation.Error("Choisissez une récurrence.")
	case it.Kind == Prepay:
		return validation.Error("Choisissez un crédit.")
	}
	return validation.Error("Choisissez un compte.")
}

// DeleteItem retire une modification d'un scénario.
func (s *Service) DeleteItem(ctx context.Context, scenarioID, itemID int64) error {
	res, err := s.db.W.ExecContext(ctx, `DELETE FROM scenario_items WHERE id = ? AND scenario_id = ?`, itemID, scenarioID)
	return affectedOne(res, err)
}

func validName(name string) (string, error) {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return name, validation.Error("Donnez un nom au scénario.")
	case utf8.RuneCountInString(name) > nameMaxLen:
		return name, validation.Errorf("Le nom ne doit pas dépasser %d caractères.", nameMaxLen)
	}
	return name, nil
}

// validItem contrôle une modification et ne garde que les champs utiles à
// sa nature.
func validItem(it Item) (Item, error) {
	if it.Date == "" {
		return it, validation.Error("Indiquez la date.")
	}
	out := Item{Kind: it.Kind, Date: it.Date}
	switch it.Kind {
	case OneOff, Repeating:
		out.Label, out.AccountID, out.Amount = strings.TrimSpace(it.Label), it.AccountID, it.Amount
		switch {
		case out.Label == "":
			return it, validation.Error("Indiquez un libellé.")
		case utf8.RuneCountInString(out.Label) > labelMaxLen:
			return it, validation.Errorf("Le libellé ne doit pas dépasser %d caractères.", labelMaxLen)
		case out.Amount == 0 || out.Amount > money.Max || out.Amount < -money.Max:
			return it, validation.Error("Indiquez un montant non nul.")
		}
		if it.Kind == Repeating {
			out.Months, out.End = it.Months, it.End
			if !out.Period().Valid() {
				return it, validation.Error("Périodicité invalide : de 1 à 24 mois entre deux échéances.")
			}
			if out.End != "" && out.End < out.Date {
				return it, validation.Error("La date de fin précède la première échéance.")
			}
		}
	case Suspend:
		out.RuleID = it.RuleID
	case Vary:
		out.RuleID, out.Amount, out.Percent = it.RuleID, it.Amount, it.Percent
		switch {
		case (out.Amount == 0) == (out.Percent == 0):
			return it, validation.Error("Indiquez la variation, en euros ou en pourcentage.")
		case out.Percent < -10_000 || out.Percent > maxPercent:
			return it, validation.Error("Variation invalide : de −100 % à +1 000 %.")
		case out.Amount > money.Max || out.Amount < -money.Max:
			return it, validation.Error("Variation trop élevée.")
		}
	case NewLoan:
		in, err := it.Loan().Validate()
		if err != nil {
			return it, err
		}
		out.Label, out.AccountID, out.Amount, out.Rate, out.Months, out.Insurance = in.Name, in.AccountID, in.Principal, in.Rate, in.Months, in.Insurance
	case Prepay:
		out.LoanID, out.Amount = it.LoanID, it.Amount
		if out.Amount <= 0 || out.Amount > money.Max {
			return it, validation.Error("Indiquez le montant remboursé.")
		}
	default:
		return it, validation.Error("Choisissez le type de modification.")
	}
	switch {
	case (out.Kind == Suspend || out.Kind == Vary) && out.RuleID == 0:
		return it, validation.Error("Choisissez une récurrence.")
	case out.Kind == Prepay && out.LoanID == 0:
		return it, validation.Error("Choisissez un crédit.")
	case (out.Kind == OneOff || out.Kind == Repeating || out.Kind == NewLoan) && out.AccountID == 0:
		return it, validation.Error("Choisissez un compte.")
	}
	return out, nil
}

// Apply renvoie le montant d'une récurrence après une variation : l'écart
// porte sur sa valeur absolue et le signe est conservé ; une baisse au-delà
// du montant l'annule (0).
func (it Item) Apply(amount int64) int64 {
	abs := max(amount, -amount)
	if it.Percent != 0 {
		abs = (abs*int64(10_000+it.Percent) + 5_000) / 10_000
	} else {
		abs = max(abs+it.Amount, 0)
	}
	if amount < 0 {
		return -abs
	}
	return abs
}

func affectedOne(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return errors.Join(err, ErrNotFound)
	}
	return nil
}
