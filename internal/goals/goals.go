// Package goals gère les projets (vacances, travaux, voiture…) : un montant
// cible à une date, rattaché à un compte d'épargne dont une part lui est
// affectée (cagnotte virtuelle, sans compte bancaire supplémentaire).
package goals

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/codecolors/buddyget/internal/ledger"
	"github.com/codecolors/buddyget/internal/platform/calendar"
	"github.com/codecolors/buddyget/internal/platform/db"
	"github.com/codecolors/buddyget/internal/platform/money"
	"github.com/codecolors/buddyget/internal/platform/validation"
	"github.com/codecolors/buddyget/internal/recurring"
)

// ErrNotFound signale un projet inexistant.
var ErrNotFound = errors.New("projet introuvable")

const nameMaxLen = 60

// Goal est un projet.
type Goal struct {
	ID          int64
	Name        string
	Target      int64
	TargetDate  calendar.Date
	AccountID   int64 // compte d'épargne de rattachement
	AccountName string
	Saved       int64  // cagnotte : part du compte affectée au projet
	Tag         string // étiquette des dépenses du projet
	RuleID      int64  // versement programmé qui l'alimente
	// Calculés à la lecture.
	Monthly int64 // versement programmé, ramené au mois
	Spent   int64 // dépenses portant l'étiquette du projet
}

// Remaining renvoie ce qui manque pour atteindre la cible.
func (g Goal) Remaining() int64 { return max(g.Target-g.Saved, 0) }

// Percent renvoie la progression, de 0 à 100.
func (g Goal) Percent() int { return int(min(g.Saved*100/g.Target, 100)) }

// MonthsLeft renvoie le nombre de mois jusqu'à la date cible (1 au moins).
func (g Goal) MonthsLeft(today calendar.Date) int {
	a, b := today.Time(), g.TargetDate.Time()
	months := (b.Year()-a.Year())*12 + int(b.Month()-a.Month())
	if b.Day() < a.Day() {
		months--
	}
	return max(months, 1)
}

// Suggested renvoie le versement mensuel qui atteint la cible à la date
// prévue.
func (g Goal) Suggested(today calendar.Date) int64 {
	n := int64(g.MonthsLeft(today))
	return (g.Remaining() + n - 1) / n
}

// Projected renvoie la date d'atteinte au rythme du versement programmé ;
// faux sans versement programmé (ou cible atteinte : aujourd'hui).
func (g Goal) Projected(today calendar.Date) (calendar.Date, bool) {
	if g.Remaining() == 0 {
		return today, true
	}
	if g.Monthly <= 0 {
		return "", false
	}
	months := int((g.Remaining() + g.Monthly - 1) / g.Monthly)
	return calendar.FromTime(today.Time().AddDate(0, months, 0)), true
}

// Late indique un projet qui, au rythme actuel, dépasse sa date cible.
func (g Goal) Late(today calendar.Date) bool {
	d, ok := g.Projected(today)
	return !ok || d > g.TargetDate
}

// Input est la saisie d'un projet.
type Input struct {
	Name       string
	Target     int64
	TargetDate calendar.Date
	AccountID  int64
	Tag        string
}

// Service gère les projets.
type Service struct {
	db        *db.DB
	ledger    *ledger.Service
	recurring *recurring.Service
}

// NewService crée le service des projets.
func NewService(d *db.DB, l *ledger.Service, r *recurring.Service) *Service {
	return &Service{db: d, ledger: l, recurring: r}
}

const goalSelect = `
	SELECT g.id, g.name, g.target, g.target_date, g.account_id, a.name, g.saved, g.tag, coalesce(g.rule_id, 0)
	FROM goals g JOIN accounts a ON a.id = g.account_id`

func scanGoal(row db.Scanner) (Goal, error) {
	var g Goal
	err := row.Scan(&g.ID, &g.Name, &g.Target, &g.TargetDate, &g.AccountID, &g.AccountName, &g.Saved, &g.Tag, &g.RuleID)
	return g, err
}

// Goals renvoie les projets, par date cible.
func (s *Service) Goals(ctx context.Context) ([]Goal, error) {
	out, err := db.Collect(ctx, s.db.R, scanGoal, goalSelect+` ORDER BY g.target_date, g.name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	for i := range out {
		if err := s.complete(ctx, &out[i]); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// Goal renvoie un projet.
func (s *Service) Goal(ctx context.Context, id int64) (Goal, error) {
	g, err := scanGoal(s.db.R.QueryRowContext(ctx, goalSelect+` WHERE g.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return g, ErrNotFound
	}
	if err != nil {
		return g, err
	}
	return g, s.complete(ctx, &g)
}

// complete calcule le versement programmé mensuel et les dépenses étiquetées.
func (s *Service) complete(ctx context.Context, g *Goal) error {
	if g.RuleID != 0 {
		rule, err := s.recurring.Rule(ctx, g.RuleID)
		if err != nil && !errors.Is(err, recurring.ErrNotFound) {
			return err
		}
		if err == nil && rule.Active {
			g.Monthly = recurring.MonthlyCost(rule)
		}
	}
	if g.Tag == "" {
		return nil
	}
	return s.db.R.QueryRowContext(ctx, `
		SELECT coalesce(-sum(t.amount), 0) FROM transactions t
		JOIN transaction_tags tt ON tt.transaction_id = t.id JOIN tags x ON x.id = tt.tag_id
		WHERE x.name = ? AND t.amount < 0`, g.Tag).Scan(&g.Spent)
}

// Create enregistre un projet.
func (s *Service) Create(ctx context.Context, in Input) (int64, error) {
	in, err := s.validate(ctx, in)
	if err != nil {
		return 0, err
	}
	res, err := s.db.W.ExecContext(ctx, `INSERT INTO goals (name, target, target_date, account_id, tag) VALUES (?, ?, ?, ?, ?)`,
		in.Name, in.Target, in.TargetDate, in.AccountID, in.Tag)
	if db.IsUniqueViolation(err) {
		return 0, validation.Error("Un projet porte déjà ce nom.")
	}
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// Update modifie un projet ; sa cagnotte est conservée.
func (s *Service) Update(ctx context.Context, id int64, in Input) error {
	in, err := s.validate(ctx, in)
	if err != nil {
		return err
	}
	res, err := s.db.W.ExecContext(ctx, `UPDATE goals SET name = ?, target = ?, target_date = ?, account_id = ?, tag = ? WHERE id = ?`,
		in.Name, in.Target, in.TargetDate, in.AccountID, in.Tag, id)
	if db.IsUniqueViolation(err) {
		return validation.Error("Un projet porte déjà ce nom.")
	}
	return affectedOne(res, err)
}

// Delete supprime un projet (son versement programmé reste, à suspendre
// au besoin).
func (s *Service) Delete(ctx context.Context, id int64) error {
	res, err := s.db.W.ExecContext(ctx, `DELETE FROM goals WHERE id = ?`, id)
	return affectedOne(res, err)
}

// Allocate ajoute (ou retire, delta négatif) une somme à la cagnotte. Les
// cagnottes d'un compte ne dépassent jamais son solde.
func (s *Service) Allocate(ctx context.Context, id, delta int64, today calendar.Date) error {
	g, err := s.Goal(ctx, id)
	if err != nil {
		return err
	}
	if delta == 0 || g.Saved+delta < 0 {
		return validation.Errorf("Indiquez un montant (au plus %s à retirer).", money.Format(g.Saved))
	}
	free, err := s.Unallocated(ctx, g.AccountID, today)
	if err != nil {
		return err
	}
	if delta > free {
		return validation.Errorf("Il ne reste que %s non affectés sur %s.", money.Format(max(free, 0)), g.AccountName)
	}
	_, err = s.db.W.ExecContext(ctx, `UPDATE goals SET saved = saved + ? WHERE id = ?`, delta, id)
	return err
}

// Unallocated renvoie la part d'un compte qu'aucun projet ne s'est affectée.
func (s *Service) Unallocated(ctx context.Context, accountID int64, today calendar.Date) (int64, error) {
	accounts, err := s.ledger.Accounts(ctx, today)
	if err != nil {
		return 0, err
	}
	var balance, allocated int64
	for _, a := range accounts {
		if a.ID == accountID {
			balance = a.Balance
		}
	}
	err = s.db.R.QueryRowContext(ctx, `SELECT coalesce(sum(saved), 0) FROM goals WHERE account_id = ?`, accountID).Scan(&allocated)
	return balance - allocated, err
}

// Schedule crée le versement programmé qui alimente le projet : chaque mois,
// du compte from vers le compte du projet, le versement suggéré, du mois
// prochain à la date cible.
func (s *Service) Schedule(ctx context.Context, id, from int64, today calendar.Date) error {
	g, err := s.Goal(ctx, id)
	if err != nil {
		return err
	}
	amount := g.Suggested(today)
	if amount <= 0 {
		return validation.Error("La cible est déjà atteinte.")
	}
	t := today.Time()
	start := calendar.FromTime(time.Date(t.Year(), t.Month()+1, min(t.Day(), 28), 0, 0, 0, 0, time.UTC))
	ruleID, err := s.recurring.CreateRule(ctx, recurring.RuleInput{
		Label: "Projet " + g.Name, AccountID: from, ToAccountID: g.AccountID, Amount: -amount, Period: calendar.Monthly,
		Shift: calendar.NextBusinessDay, Start: start, End: g.TargetDate, Origin: recurring.Manual,
	})
	if err != nil {
		return err
	}
	_, err = s.db.W.ExecContext(ctx, `UPDATE goals SET rule_id = ? WHERE id = ?`, ruleID, id)
	return err
}

func (s *Service) validate(ctx context.Context, in Input) (Input, error) {
	in.Name, in.Tag = strings.TrimSpace(in.Name), strings.TrimSpace(in.Tag)
	switch {
	case in.Name == "":
		return in, validation.Error("Donnez un nom au projet.")
	case utf8.RuneCountInString(in.Name) > nameMaxLen:
		return in, validation.Errorf("Le nom ne doit pas dépasser %d caractères.", nameMaxLen)
	case in.Target <= 0 || in.Target > money.Max:
		return in, validation.Error("Indiquez le montant visé.")
	case in.TargetDate == "":
		return in, validation.Error("Indiquez la date visée.")
	}
	if in.Tag != "" {
		tags, err := ledger.ParseTags(in.Tag)
		if err != nil || len(tags) != 1 {
			return in, validation.Error("Une seule étiquette pour les dépenses du projet.")
		}
		in.Tag = tags[0]
	}
	a, err := s.ledger.Account(ctx, in.AccountID)
	if errors.Is(err, ledger.ErrNotFound) || err == nil && a.Kind != ledger.AccountSavings {
		return in, validation.Error("Choisissez le compte d'épargne du projet.")
	}
	return in, err
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
