package ledger

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"

	"github.com/codecolors/buddyget/internal/platform/calendar"
	"github.com/codecolors/buddyget/internal/platform/db"
	"github.com/codecolors/buddyget/internal/platform/money"
	"github.com/codecolors/buddyget/internal/platform/text"
	"github.com/codecolors/buddyget/internal/platform/validation"
)

const accountNameMaxLen = 60

// AccountKind est le type d'un compte.
type AccountKind string

const (
	AccountCurrent AccountKind = "courant"
	AccountSavings AccountKind = "epargne"
	AccountCash    AccountKind = "especes"
	AccountOther   AccountKind = "autre"
)

// AccountKinds liste les types dans l'ordre d'affichage.
var AccountKinds = []AccountKind{AccountCurrent, AccountSavings, AccountCash, AccountOther}

// Label renvoie le libellé affiché.
func (k AccountKind) Label() string {
	switch k {
	case AccountCurrent:
		return "Compte courant"
	case AccountSavings:
		return "Épargne"
	case AccountCash:
		return "Espèces"
	}
	return "Autre"
}

// Bank est l'établissement d'un compte ; il guidera le choix du profil
// d'import.
type Bank string

const (
	BankSG    Bank = "SG"
	BankBNP   Bank = "BNP"
	BankOther Bank = "autre"
)

// Banks liste les établissements dans l'ordre d'affichage.
var Banks = []Bank{BankSG, BankBNP, BankOther}

// Label renvoie le libellé affiché.
func (b Bank) Label() string {
	switch b {
	case BankSG:
		return "Société Générale"
	case BankBNP:
		return "BNP Paribas"
	}
	return "Autre banque"
}

// Account est un compte du foyer. OpeningBalance est le solde au début du
// jour OpeningDate : les opérations datées de ce jour ou après s'y ajoutent.
type Account struct {
	ID             int64
	Name           string
	Kind           AccountKind
	Bank           Bank
	IBAN           string // masqué : « FR76 •••• 1234 »
	OpeningBalance int64
	OpeningDate    calendar.Date
	Archived       bool
}

// AccountBalance associe un compte à son solde à une date.
type AccountBalance struct {
	Account
	Balance int64
}

// AccountInput est la saisie d'un compte. IBAN est soit un IBAN complet
// (masqué avant stockage), soit la valeur masquée actuelle (inchangée).
type AccountInput struct {
	Name           string
	Kind           AccountKind
	Bank           Bank
	IBAN           string
	OpeningBalance int64
	OpeningDate    calendar.Date
}

const accountColumns = `a.id, a.name, a.kind, a.bank, a.iban_masked, a.opening_balance, a.opening_date, a.archived`

func scanAccount(row interface{ Scan(...any) error }, extra ...any) (Account, error) {
	var a Account
	err := row.Scan(append([]any{&a.ID, &a.Name, &a.Kind, &a.Bank, &a.IBAN, &a.OpeningBalance, &a.OpeningDate, &a.Archived}, extra...)...)
	return a, err
}

// Accounts renvoie tous les comptes avec leur solde au soir du jour donné :
// actifs d'abord, puis archivés, chacun par ordre alphabétique.
func (s *Service) Accounts(ctx context.Context, on calendar.Date) ([]AccountBalance, error) {
	rows, err := s.db.R.QueryContext(ctx, `
		SELECT `+accountColumns+`, a.opening_balance + coalesce((
			SELECT sum(t.amount) FROM transactions t
			WHERE t.account_id = a.id AND t.date >= a.opening_date AND t.date <= ?
		), 0)
		FROM accounts a`, on)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []AccountBalance
	for rows.Next() {
		var ab AccountBalance
		if ab.Account, err = scanAccount(rows, &ab.Balance); err != nil {
			return nil, err
		}
		out = append(out, ab)
	}
	slices.SortFunc(out, func(a, b AccountBalance) int {
		if a.Archived != b.Archived {
			if a.Archived {
				return 1
			}
			return -1
		}
		return strings.Compare(text.Fold(a.Name), text.Fold(b.Name))
	})
	return out, rows.Err()
}

// Account renvoie un compte.
func (s *Service) Account(ctx context.Context, id int64) (Account, error) {
	a, err := scanAccount(s.db.R.QueryRowContext(ctx, `SELECT `+accountColumns+` FROM accounts a WHERE a.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Account{}, ErrNotFound
	}
	return a, err
}

// CreateAccount crée un compte et renvoie son identifiant.
func (s *Service) CreateAccount(ctx context.Context, in AccountInput) (int64, error) {
	in, err := validateAccount(in, "")
	if err != nil {
		return 0, err
	}
	res, err := s.db.W.ExecContext(ctx, `
		INSERT INTO accounts (name, kind, bank, iban_masked, opening_balance, opening_date)
		VALUES (?, ?, ?, ?, ?, ?)`,
		in.Name, in.Kind, in.Bank, in.IBAN, in.OpeningBalance, in.OpeningDate)
	if err != nil {
		return 0, accountWriteError(err)
	}
	return res.LastInsertId()
}

// UpdateAccount modifie un compte.
func (s *Service) UpdateAccount(ctx context.Context, id int64, in AccountInput) error {
	cur, err := s.Account(ctx, id)
	if err != nil {
		return err
	}
	if in, err = validateAccount(in, cur.IBAN); err != nil {
		return err
	}
	_, err = s.db.W.ExecContext(ctx, `
		UPDATE accounts SET name = ?, kind = ?, bank = ?, iban_masked = ?, opening_balance = ?, opening_date = ?
		WHERE id = ?`,
		in.Name, in.Kind, in.Bank, in.IBAN, in.OpeningBalance, in.OpeningDate, id)
	return accountWriteError(err)
}

// SetAccountArchived archive ou réactive un compte : un compte archivé
// disparaît des listes de saisie mais garde son historique.
func (s *Service) SetAccountArchived(ctx context.Context, id int64, archived bool) error {
	res, err := s.db.W.ExecContext(ctx, `UPDATE accounts SET archived = ? WHERE id = ?`, archived, id)
	return affectedOne(res, err)
}

// DeleteAccount supprime un compte sans opération.
func (s *Service) DeleteAccount(ctx context.Context, id int64) error {
	return s.db.Tx(ctx, func(tx *sql.Tx) error {
		used, err := exists(ctx, tx, `SELECT EXISTS (SELECT 1 FROM transactions WHERE account_id = ?)`, id)
		if err != nil {
			return err
		}
		if used {
			return validation.Error("Ce compte contient des opérations : archivez-le plutôt que de le supprimer.")
		}
		res, err := tx.ExecContext(ctx, `DELETE FROM accounts WHERE id = ?`, id)
		return affectedOne(res, err)
	})
}

func validateAccount(in AccountInput, currentIBAN string) (AccountInput, error) {
	var err error
	if in.Name, err = cleanText(in.Name, "le nom du compte", accountNameMaxLen, true); err != nil {
		return in, err
	}
	if !slices.Contains(AccountKinds, in.Kind) {
		return in, validation.Error("Choisissez le type de compte.")
	}
	if !slices.Contains(Banks, in.Bank) {
		return in, validation.Error("Choisissez la banque.")
	}
	if in.OpeningBalance > money.Max || in.OpeningBalance < -money.Max {
		return in, validation.Error("Solde initial trop élevé.")
	}
	if in.OpeningDate == "" {
		return in, validation.Error("Indiquez la date du solde initial.")
	}
	if in.IBAN = strings.TrimSpace(in.IBAN); in.IBAN != currentIBAN {
		in.IBAN, err = maskIBAN(in.IBAN)
	}
	return in, err
}

// maskIBAN ne garde que le code pays, la clé et les quatre derniers
// caractères : assez pour reconnaître le compte, inutilisable sinon.
func maskIBAN(raw string) (string, error) {
	iban := strings.ToUpper(strings.Join(strings.Fields(raw), ""))
	if iban == "" {
		return "", nil
	}
	valid := len(iban) >= 15 && len(iban) <= 34
	for i := range len(iban) {
		c := iban[i]
		valid = valid && (c >= 'A' && c <= 'Z' || c >= '0' && c <= '9')
	}
	if !valid {
		return "", validation.Error("IBAN invalide : 15 à 34 lettres et chiffres.")
	}
	return iban[:4] + " •••• " + iban[len(iban)-4:], nil
}

func accountWriteError(err error) error {
	if db.IsUniqueViolation(err) {
		return validation.Error("Un compte porte déjà ce nom.")
	}
	return err
}

// affectedOne transforme une écriture sans effet en ErrNotFound.
func affectedOne(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err == nil && n == 0 {
		return ErrNotFound
	}
	return err
}
