package ledger

import (
	"context"
	"database/sql"
	"errors"

	"github.com/codecolors/buddyget/internal/platform/calendar"
	"github.com/codecolors/buddyget/internal/platform/money"
	"github.com/codecolors/buddyget/internal/platform/validation"
)

const (
	labelMaxLen = 200
	noteMaxLen  = 1000
)

// Transaction est une opération, enrichie des noms utiles à l'affichage.
// Pour une jambe de virement, Counterpart* désigne l'autre compte.
type Transaction struct {
	ID                     int64
	AccountID              int64
	AccountName            string
	Date                   calendar.Date
	Label                  string
	Amount                 int64
	CategoryID             int64
	CategoryName           string
	CategoryColor          string
	Note                   string
	Cleared                bool
	TransferID             int64
	CounterpartAccountID   int64
	CounterpartAccountName string
}

// IsTransfer indique une jambe de virement entre comptes du foyer.
func (t Transaction) IsTransfer() bool { return t.TransferID != 0 }

// TransactionInput est la saisie d'une opération simple ; Amount est signé.
type TransactionInput struct {
	AccountID  int64
	Date       calendar.Date
	Label      string
	Amount     int64
	CategoryID int64
	Note       string
	Cleared    bool
}

// TransferInput est la saisie d'un virement ; Amount est positif et quitte
// FromAccountID pour ToAccountID.
type TransferInput struct {
	FromAccountID int64
	ToAccountID   int64
	Date          calendar.Date
	Label         string
	Amount        int64
	Note          string
}

// transactionSelect lit les opérations avec leur compte, leur catégorie
// (« Parent › Enfant ») et, pour un virement, le compte opposé.
const transactionSelect = `
	SELECT t.id, t.account_id, a.name, t.date, t.label, t.amount,
		coalesce(t.category_id, 0),
		coalesce(CASE WHEN p.id IS NULL THEN c.name ELSE p.name || ' › ' || c.name END, ''),
		coalesce(c.color, ''), t.note, t.cleared, coalesce(t.transfer_id, 0),
		coalesce(o.account_id, 0), coalesce(oa.name, '')
	FROM transactions t
	JOIN accounts a ON a.id = t.account_id
	LEFT JOIN categories c ON c.id = t.category_id
	LEFT JOIN categories p ON p.id = c.parent_id
	LEFT JOIN transactions o ON o.transfer_id = t.transfer_id AND o.id <> t.id
	LEFT JOIN accounts oa ON oa.id = o.account_id`

func scanTransaction(row interface{ Scan(...any) error }) (Transaction, error) {
	var t Transaction
	err := row.Scan(&t.ID, &t.AccountID, &t.AccountName, &t.Date, &t.Label, &t.Amount,
		&t.CategoryID, &t.CategoryName, &t.CategoryColor, &t.Note, &t.Cleared, &t.TransferID,
		&t.CounterpartAccountID, &t.CounterpartAccountName)
	return t, err
}

// Transaction renvoie une opération.
func (s *Service) Transaction(ctx context.Context, id int64) (Transaction, error) {
	t, err := scanTransaction(s.db.R.QueryRowContext(ctx, transactionSelect+` WHERE t.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Transaction{}, ErrNotFound
	}
	return t, err
}

// CreateTransaction enregistre une opération et renvoie son identifiant.
func (s *Service) CreateTransaction(ctx context.Context, in TransactionInput) (int64, error) {
	var id int64
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		var err error
		if in, err = validateTransaction(ctx, tx, in); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `
			INSERT INTO transactions (account_id, date, label, amount, category_id, note, cleared)
			VALUES (?, ?, ?, ?, nullif(?, 0), ?, ?)`,
			in.AccountID, in.Date, in.Label, in.Amount, in.CategoryID, in.Note, in.Cleared)
		if err != nil {
			return err
		}
		id, err = res.LastInsertId()
		return err
	})
	return id, err
}

// UpdateTransaction modifie une opération simple.
func (s *Service) UpdateTransaction(ctx context.Context, id int64, in TransactionInput) error {
	return s.db.Tx(ctx, func(tx *sql.Tx) error {
		var transferID int64
		err := tx.QueryRowContext(ctx, `SELECT coalesce(transfer_id, 0) FROM transactions WHERE id = ?`, id).Scan(&transferID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if transferID != 0 {
			return validation.Error("Cette opération fait partie d'un virement : modifiez le virement.")
		}
		if in, err = validateTransaction(ctx, tx, in); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `
			UPDATE transactions SET account_id = ?, date = ?, label = ?, amount = ?,
				category_id = nullif(?, 0), note = ?, cleared = ?
			WHERE id = ?`,
			in.AccountID, in.Date, in.Label, in.Amount, in.CategoryID, in.Note, in.Cleared, id)
		return err
	})
}

// CreateTransfer enregistre un virement : une sortie sur le compte source et
// une entrée sur le compte destinataire, reliées. Renvoie l'identifiant de
// la sortie.
func (s *Service) CreateTransfer(ctx context.Context, in TransferInput) (int64, error) {
	var id int64
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		var err error
		if in, err = validateTransfer(ctx, tx, in); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO transfers DEFAULT VALUES`)
		if err != nil {
			return err
		}
		transferID, err := res.LastInsertId()
		if err != nil {
			return err
		}
		insert := `INSERT INTO transactions (account_id, date, label, amount, note, transfer_id) VALUES (?, ?, ?, ?, ?, ?)`
		if res, err = tx.ExecContext(ctx, insert, in.FromAccountID, in.Date, in.Label, -in.Amount, in.Note, transferID); err != nil {
			return err
		}
		if id, err = res.LastInsertId(); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, insert, in.ToAccountID, in.Date, in.Label, in.Amount, in.Note, transferID)
		return err
	})
	return id, err
}

// UpdateTransfer modifie les deux jambes d'un virement ; leur pointage est
// conservé.
func (s *Service) UpdateTransfer(ctx context.Context, transferID int64, in TransferInput) error {
	return s.db.Tx(ctx, func(tx *sql.Tx) error {
		ok, err := exists(ctx, tx, `SELECT EXISTS (SELECT 1 FROM transfers WHERE id = ?)`, transferID)
		if err != nil {
			return err
		}
		if !ok {
			return ErrNotFound
		}
		if in, err = validateTransfer(ctx, tx, in); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `
			UPDATE transactions SET
				account_id = CASE WHEN amount < 0 THEN ? ELSE ? END,
				amount = CASE WHEN amount < 0 THEN ? ELSE ? END,
				date = ?, label = ?, note = ?
			WHERE transfer_id = ?`,
			in.FromAccountID, in.ToAccountID, -in.Amount, in.Amount, in.Date, in.Label, in.Note, transferID)
		return err
	})
}

// DeleteTransaction supprime une opération ; pour un virement, les deux
// jambes sont supprimées.
func (s *Service) DeleteTransaction(ctx context.Context, id int64) error {
	return s.db.Tx(ctx, func(tx *sql.Tx) error {
		var transferID int64
		err := tx.QueryRowContext(ctx, `SELECT coalesce(transfer_id, 0) FROM transactions WHERE id = ?`, id).Scan(&transferID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if transferID != 0 {
			_, err = tx.ExecContext(ctx, `DELETE FROM transfers WHERE id = ?`, transferID)
		} else {
			_, err = tx.ExecContext(ctx, `DELETE FROM transactions WHERE id = ?`, id)
		}
		return err
	})
}

// RecentLabels renvoie les libellés distincts des dernières opérations, du
// plus récent au plus ancien, pour l'autocomplétion de la saisie.
func (s *Service) RecentLabels(ctx context.Context, limit int) ([]string, error) {
	rows, err := s.db.R.QueryContext(ctx, `
		SELECT label FROM (
			SELECT id, label FROM transactions WHERE transfer_id IS NULL ORDER BY id DESC LIMIT 1000
		) GROUP BY label ORDER BY max(id) DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var l string
		if err := rows.Scan(&l); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// LastUsedAccount renvoie le compte de la dernière opération saisie sur un
// compte actif (0 si aucune), proposé par défaut à la saisie suivante.
func (s *Service) LastUsedAccount(ctx context.Context) (int64, error) {
	var id int64
	err := s.db.R.QueryRowContext(ctx, `
		SELECT t.account_id FROM transactions t JOIN accounts a ON a.id = t.account_id
		WHERE a.archived = 0 ORDER BY t.id DESC LIMIT 1`).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return id, err
}

func validateTransaction(ctx context.Context, tx *sql.Tx, in TransactionInput) (TransactionInput, error) {
	var err error
	if in.Label, err = cleanText(in.Label, "un libellé", labelMaxLen, true); err != nil {
		return in, err
	}
	if in.Note, err = cleanText(in.Note, "la note", noteMaxLen, false); err != nil {
		return in, err
	}
	if in.Amount == 0 {
		return in, validation.Error("Indiquez un montant non nul.")
	}
	if in.Amount > money.Max || in.Amount < -money.Max {
		return in, validation.Error("Montant trop élevé.")
	}
	if in.Date == "" {
		return in, validation.Error("Indiquez une date.")
	}
	if err := checkAccount(ctx, tx, in.AccountID); err != nil {
		return in, err
	}
	if in.CategoryID != 0 {
		ok, err := exists(ctx, tx, `SELECT EXISTS (SELECT 1 FROM categories WHERE id = ?)`, in.CategoryID)
		if err != nil {
			return in, err
		}
		if !ok {
			return in, validation.Error("Catégorie introuvable.")
		}
	}
	return in, nil
}

func validateTransfer(ctx context.Context, tx *sql.Tx, in TransferInput) (TransferInput, error) {
	var err error
	if in.Label, err = cleanText(in.Label, "un libellé", labelMaxLen, true); err != nil {
		return in, err
	}
	if in.Note, err = cleanText(in.Note, "la note", noteMaxLen, false); err != nil {
		return in, err
	}
	if in.Amount <= 0 {
		return in, validation.Error("Indiquez un montant positif.")
	}
	if in.Amount > money.Max {
		return in, validation.Error("Montant trop élevé.")
	}
	if in.Date == "" {
		return in, validation.Error("Indiquez une date.")
	}
	if in.FromAccountID == in.ToAccountID {
		return in, validation.Error("Choisissez deux comptes différents.")
	}
	if err := checkAccount(ctx, tx, in.FromAccountID); err != nil {
		return in, err
	}
	return in, checkAccount(ctx, tx, in.ToAccountID)
}

func checkAccount(ctx context.Context, tx *sql.Tx, id int64) error {
	ok, err := exists(ctx, tx, `SELECT EXISTS (SELECT 1 FROM accounts WHERE id = ?)`, id)
	if err != nil {
		return err
	}
	if !ok {
		return validation.Error("Choisissez un compte.")
	}
	return nil
}
