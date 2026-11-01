package loans

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/codecolors/buddyget/internal/platform/calendar"
	"github.com/codecolors/buddyget/internal/platform/db"
	"github.com/codecolors/buddyget/internal/platform/money"
	"github.com/codecolors/buddyget/internal/platform/validation"
	"github.com/codecolors/buddyget/internal/recurring"
)

// ErrNotFound signale un crédit inexistant.
var ErrNotFound = errors.New("crédit introuvable")

const nameMaxLen = 60

// Loan est un crédit suivi.
type Loan struct {
	ID          int64
	Name        string
	AccountID   int64 // compte où la mensualité est prélevée
	AccountName string
	Params
	Prepayments []Prepayment
	RuleID      int64 // récurrence de la mensualité
}

// Schedule renvoie le tableau d'amortissement du crédit.
func (l Loan) Schedule() Schedule { return Amortize(l.Params, l.Prepayments) }

// Rule renvoie la récurrence de la mensualité (assurance comprise), jusqu'à
// la dernière échéance du tableau d'amortissement.
func (l Loan) Rule() recurring.RuleInput {
	sched := l.Schedule()
	return recurring.RuleInput{
		Label: "Crédit " + l.Name, AccountID: l.AccountID, Amount: -(sched.Payment + l.Insurance), Period: calendar.Monthly,
		Shift: calendar.NextBusinessDay, Start: l.First, End: sched.End, Origin: recurring.Manual,
	}
}

// Input est la saisie d'un crédit.
type Input struct {
	Name      string
	AccountID int64
	Params
}

// Validate contrôle la saisie, indépendamment du compte.
func (in Input) Validate() (Input, error) {
	in.Name = strings.TrimSpace(in.Name)
	switch {
	case in.Name == "":
		return in, validation.Error("Donnez un nom au crédit.")
	case utf8.RuneCountInString(in.Name) > nameMaxLen:
		return in, validation.Errorf("Le nom ne doit pas dépasser %d caractères.", nameMaxLen)
	case in.Principal <= 0 || in.Principal > money.Max:
		return in, validation.Error("Indiquez le capital emprunté.")
	case in.Rate < 0 || in.Rate > 100_000:
		return in, validation.Error("Taux invalide : entre 0 et 100 %.")
	case in.Months < 1 || in.Months > maxRows:
		return in, validation.Errorf("Durée invalide : de 1 à %d mois.", maxRows)
	case in.Insurance < 0 || in.Insurance > money.Max:
		return in, validation.Error("Assurance invalide.")
	case in.First == "":
		return in, validation.Error("Indiquez la date de la première échéance.")
	}
	return in, nil
}

// Service gère les crédits.
type Service struct {
	db        *db.DB
	recurring *recurring.Service
}

// NewService crée le service des crédits.
func NewService(d *db.DB, r *recurring.Service) *Service { return &Service{db: d, recurring: r} }

const loanSelect = `
	SELECT l.id, l.name, l.account_id, a.name, l.principal, l.rate, l.months, l.insurance, l.first_payment, coalesce(l.rule_id, 0)
	FROM loans l JOIN accounts a ON a.id = l.account_id`

func scanLoan(row db.Scanner) (Loan, error) {
	var l Loan
	err := row.Scan(&l.ID, &l.Name, &l.AccountID, &l.AccountName, &l.Principal, &l.Rate, &l.Months, &l.Insurance, &l.First, &l.RuleID)
	return l, err
}

// Loans renvoie les crédits, par nom, avec leurs remboursements anticipés.
func (s *Service) Loans(ctx context.Context) ([]Loan, error) {
	out, err := db.Collect(ctx, s.db.R, scanLoan, loanSelect+` ORDER BY l.name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	for i := range out {
		if out[i].Prepayments, err = s.prepayments(ctx, out[i].ID); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// Loan renvoie un crédit.
func (s *Service) Loan(ctx context.Context, id int64) (Loan, error) {
	l, err := scanLoan(s.db.R.QueryRowContext(ctx, loanSelect+` WHERE l.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return l, ErrNotFound
	}
	if err != nil {
		return l, err
	}
	l.Prepayments, err = s.prepayments(ctx, id)
	return l, err
}

func (s *Service) prepayments(ctx context.Context, loanID int64) ([]Prepayment, error) {
	return db.Collect(ctx, s.db.R, func(row db.Scanner) (Prepayment, error) {
		var p Prepayment
		err := row.Scan(&p.Date, &p.Amount)
		return p, err
	}, `SELECT date, amount FROM loan_prepayments WHERE loan_id = ? ORDER BY date, id`, loanID)
}

// Create enregistre un crédit et la récurrence de sa mensualité.
func (s *Service) Create(ctx context.Context, in Input) (int64, error) {
	in, err := in.Validate()
	if err != nil {
		return 0, err
	}
	res, err := s.db.W.ExecContext(ctx, `
		INSERT INTO loans (name, account_id, principal, rate, months, insurance, first_payment) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		in.Name, in.AccountID, in.Principal, in.Rate, in.Months, in.Insurance, in.First)
	if err := writeError(err); err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	return id, s.syncRule(ctx, id)
}

// Update modifie un crédit et la récurrence de sa mensualité.
func (s *Service) Update(ctx context.Context, id int64, in Input) error {
	in, err := in.Validate()
	if err != nil {
		return err
	}
	res, err := s.db.W.ExecContext(ctx, `
		UPDATE loans SET name = ?, account_id = ?, principal = ?, rate = ?, months = ?, insurance = ?, first_payment = ? WHERE id = ?`,
		in.Name, in.AccountID, in.Principal, in.Rate, in.Months, in.Insurance, in.First, id)
	if err := writeError(err); err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return errors.Join(err, ErrNotFound)
	}
	return s.syncRule(ctx, id)
}

// Prepay enregistre un remboursement anticipé : la récurrence de la
// mensualité s'arrête plus tôt.
func (s *Service) Prepay(ctx context.Context, id int64, p Prepayment) error {
	if p.Amount <= 0 || p.Amount > money.Max || p.Date == "" {
		return validation.Error("Indiquez la date et le montant du remboursement anticipé.")
	}
	_, err := s.db.W.ExecContext(ctx, `INSERT INTO loan_prepayments (loan_id, date, amount) VALUES (?, ?, ?)`, id, p.Date, p.Amount)
	if db.IsForeignKeyViolation(err) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	return s.syncRule(ctx, id)
}

// Delete supprime un crédit et la récurrence de sa mensualité.
func (s *Service) Delete(ctx context.Context, id int64) error {
	l, err := s.Loan(ctx, id)
	if err != nil {
		return err
	}
	if _, err := s.db.W.ExecContext(ctx, `DELETE FROM loans WHERE id = ?`, id); err != nil {
		return err
	}
	if err := s.recurring.DeleteRule(ctx, l.RuleID); err != nil && !errors.Is(err, recurring.ErrNotFound) {
		return err
	}
	return nil
}

// syncRule crée, met à jour ou supprime la récurrence de la mensualité.
func (s *Service) syncRule(ctx context.Context, id int64) error {
	l, err := s.Loan(ctx, id)
	if err != nil {
		return err
	}
	in := l.Rule()
	if in.End < in.Start {
		// Soldé avant la première échéance : plus de mensualité.
		if err := s.recurring.DeleteRule(ctx, l.RuleID); err != nil && !errors.Is(err, recurring.ErrNotFound) {
			return err
		}
		return nil
	}
	if l.RuleID != 0 {
		err := s.recurring.UpdateRule(ctx, l.RuleID, in)
		if !errors.Is(err, recurring.ErrNotFound) {
			return err
		}
	}
	ruleID, err := s.recurring.CreateRule(ctx, in)
	if err != nil {
		return err
	}
	_, err = s.db.W.ExecContext(ctx, `UPDATE loans SET rule_id = ? WHERE id = ?`, ruleID, id)
	return err
}

func writeError(err error) error {
	switch {
	case db.IsUniqueViolation(err):
		return validation.Error("Un crédit porte déjà ce nom.")
	case db.IsForeignKeyViolation(err):
		return validation.Error("Choisissez le compte où la mensualité est prélevée.")
	}
	return err
}
