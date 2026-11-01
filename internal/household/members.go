// Package household gère le partage du foyer : ses membres (distincts des
// comptes utilisateurs), le payeur et la répartition des opérations
// partagées, ce que chacun doit et les remboursements entre membres.
package household

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/codecolors/buddyget/internal/ledger"
	"github.com/codecolors/buddyget/internal/platform/db"
	"github.com/codecolors/buddyget/internal/platform/money"
	"github.com/codecolors/buddyget/internal/platform/validation"
)

// ErrNotFound signale un membre, une opération partagée ou un remboursement
// inexistants.
var ErrNotFound = errors.New("élément du partage introuvable")

const nameMaxLen = 40

// Member est un membre du foyer.
type Member struct {
	ID       int64
	Name     string
	Income   int64 // revenu mensuel, pour la répartition au prorata
	Archived bool
}

// MemberInput est la saisie d'un membre.
type MemberInput struct {
	Name   string
	Income int64
}

// Service gère le partage du foyer.
type Service struct {
	db     *db.DB
	ledger *ledger.Service
}

// NewService crée le service du partage.
func NewService(d *db.DB, l *ledger.Service) *Service { return &Service{db: d, ledger: l} }

func scanMember(row db.Scanner) (Member, error) {
	var m Member
	err := row.Scan(&m.ID, &m.Name, &m.Income, &m.Archived)
	return m, err
}

// Members renvoie les membres, actifs d'abord, par nom.
func (s *Service) Members(ctx context.Context) ([]Member, error) {
	return db.Collect(ctx, s.db.R, scanMember,
		`SELECT id, name, income, archived FROM household_members ORDER BY archived, name COLLATE NOCASE`)
}

// Member renvoie un membre.
func (s *Service) Member(ctx context.Context, id int64) (Member, error) {
	m, err := scanMember(s.db.R.QueryRowContext(ctx, `SELECT id, name, income, archived FROM household_members WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return m, ErrNotFound
	}
	return m, err
}

// CreateMember ajoute un membre.
func (s *Service) CreateMember(ctx context.Context, in MemberInput) (int64, error) {
	in, err := in.validate()
	if err != nil {
		return 0, err
	}
	res, err := s.db.W.ExecContext(ctx, `INSERT INTO household_members (name, income) VALUES (?, ?)`, in.Name, in.Income)
	if db.IsUniqueViolation(err) {
		return 0, validation.Error("Un membre porte déjà ce nom.")
	}
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateMember modifie un membre ; les répartitions déjà enregistrées ne
// changent pas.
func (s *Service) UpdateMember(ctx context.Context, id int64, in MemberInput) error {
	in, err := in.validate()
	if err != nil {
		return err
	}
	res, err := s.db.W.ExecContext(ctx, `UPDATE household_members SET name = ?, income = ? WHERE id = ?`, in.Name, in.Income, id)
	if db.IsUniqueViolation(err) {
		return validation.Error("Un membre porte déjà ce nom.")
	}
	return affectedOne(res, err)
}

// SetArchived retire un membre des nouvelles répartitions, ou l'y rend.
func (s *Service) SetArchived(ctx context.Context, id int64, archived bool) error {
	res, err := s.db.W.ExecContext(ctx, `UPDATE household_members SET archived = ? WHERE id = ?`, archived, id)
	return affectedOne(res, err)
}

// DeleteMember supprime un membre qui n'apparaît dans aucun partage.
func (s *Service) DeleteMember(ctx context.Context, id int64) error {
	res, err := s.db.W.ExecContext(ctx, `DELETE FROM household_members WHERE id = ?`, id)
	if db.IsForeignKeyViolation(err) {
		return validation.Error("Ce membre apparaît dans des partages : archivez-le plutôt.")
	}
	return affectedOne(res, err)
}

func (in MemberInput) validate() (MemberInput, error) {
	in.Name = strings.TrimSpace(in.Name)
	switch {
	case in.Name == "":
		return in, validation.Error("Indiquez le prénom du membre.")
	case utf8.RuneCountInString(in.Name) > nameMaxLen:
		return in, validation.Errorf("Le prénom ne doit pas dépasser %d caractères.", nameMaxLen)
	case in.Income < 0 || in.Income > money.Max:
		return in, validation.Error("Revenu invalide.")
	}
	return in, nil
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
