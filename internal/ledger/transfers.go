package ledger

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/codecolors/buddyget/internal/platform/db"
	"github.com/codecolors/buddyget/internal/platform/validation"
)

// TransferWindowDays est l'écart de dates toléré entre le débit et le
// crédit d'un virement (délai de traitement entre deux banques).
const TransferWindowDays = 3

// TransferCandidate est une paire d'opérations qui ressemble à un virement
// entre deux comptes du foyer : même montant de signe opposé, dates proches.
type TransferCandidate struct {
	Debit  Transaction
	Credit Transaction
}

// TransferCandidates renvoie au plus limit paires à rapprocher, les plus
// récentes d'abord. Une paire écartée par l'utilisateur n'est plus proposée.
func (s *Service) TransferCandidates(ctx context.Context, limit int) ([]TransferCandidate, error) {
	pairs, err := s.candidatePairs(ctx, limit)
	if err != nil || len(pairs) == 0 {
		return nil, err
	}
	ids := make([]int64, 0, 2*len(pairs))
	for _, p := range pairs {
		ids = append(ids, p[0], p[1])
	}
	byID, err := s.transactionsByID(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make([]TransferCandidate, len(pairs))
	for i, p := range pairs {
		out[i] = TransferCandidate{Debit: byID[p[0]], Credit: byID[p[1]]}
	}
	return out, nil
}

// CountTransferCandidates renvoie le nombre de paires à rapprocher, borné à
// limit, sans charger les opérations.
func (s *Service) CountTransferCandidates(ctx context.Context, limit int) (int, error) {
	pairs, err := s.candidatePairs(ctx, limit)
	return len(pairs), err
}

// candidatePairs renvoie les identifiants (débit, crédit) des paires.
func (s *Service) candidatePairs(ctx context.Context, limit int) ([][2]int64, error) {
	rows, err := s.db.R.QueryContext(ctx, `
		SELECT d.id, c.id FROM transactions d
		JOIN transactions c ON c.amount = -d.amount AND c.account_id <> d.account_id
			AND c.date BETWEEN date(d.date, ?) AND date(d.date, ?)
			AND c.transfer_id IS NULL
		WHERE d.amount < 0 AND d.transfer_id IS NULL
			AND NOT EXISTS (SELECT 1 FROM transfer_dismissals x WHERE x.debit_id = d.id AND x.credit_id = c.id)
		ORDER BY d.date DESC, d.id DESC, abs(julianday(c.date) - julianday(d.date)), c.id
		LIMIT ?`,
		fmt.Sprintf("%+d days", -TransferWindowDays), fmt.Sprintf("%+d days", TransferWindowDays), limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var pairs [][2]int64
	for rows.Next() {
		var p [2]int64
		if err := rows.Scan(&p[0], &p[1]); err != nil {
			return nil, err
		}
		pairs = append(pairs, p)
	}
	return pairs, rows.Err()
}

func (s *Service) transactionsByID(ctx context.Context, ids []int64) (map[int64]Transaction, error) {
	list, err := json.Marshal(ids)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.R.QueryContext(ctx, transactionSelect+` WHERE t.id IN (SELECT value FROM json_each(?))`, string(list))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make(map[int64]Transaction, len(ids))
	for rows.Next() {
		t, err := scanTransaction(rows)
		if err != nil {
			return nil, err
		}
		out[t.ID] = t
	}
	return out, rows.Err()
}

// LinkTransfer relie deux opérations existantes en un virement : elles
// perdent leur catégorie et ne comptent plus ni en dépense ni en revenu.
func (s *Service) LinkTransfer(ctx context.Context, debitID, creditID int64) error {
	return s.db.Tx(ctx, func(tx *sql.Tx) error {
		type leg struct {
			account, amount, transfer int64
		}
		var legs [2]leg
		for i, id := range []int64{debitID, creditID} {
			err := tx.QueryRowContext(ctx,
				`SELECT account_id, amount, coalesce(transfer_id, 0) FROM transactions WHERE id = ?`, id,
			).Scan(&legs[i].account, &legs[i].amount, &legs[i].transfer)
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			if err != nil {
				return err
			}
		}
		switch d, c := legs[0], legs[1]; {
		case d.transfer != 0 || c.transfer != 0:
			return validation.Error("L'une de ces opérations fait déjà partie d'un virement.")
		case d.amount >= 0 || c.amount != -d.amount:
			return validation.Error("Un virement relie une sortie et une entrée du même montant.")
		case d.account == c.account:
			return validation.Error("Un virement relie deux comptes différents.")
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO transfers DEFAULT VALUES`)
		if err != nil {
			return err
		}
		transferID, err := res.LastInsertId()
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx,
			`UPDATE transactions SET transfer_id = ?, category_id = NULL WHERE id IN (?, ?)`,
			transferID, debitID, creditID)
		return err
	})
}

// DismissTransferCandidate écarte une paire : elle ne sera plus proposée.
func (s *Service) DismissTransferCandidate(ctx context.Context, debitID, creditID int64) error {
	_, err := s.db.W.ExecContext(ctx,
		`INSERT OR IGNORE INTO transfer_dismissals (debit_id, credit_id) VALUES (?, ?)`, debitID, creditID)
	if db.IsForeignKeyViolation(err) {
		return ErrNotFound
	}
	return err
}

// UnlinkTransfer défait un virement : ses deux opérations redeviennent
// simples, et la paire n'est plus proposée comme virement.
func (s *Service) UnlinkTransfer(ctx context.Context, transferID int64) error {
	return s.db.Tx(ctx, func(tx *sql.Tx) error {
		var debit, credit int64
		err := tx.QueryRowContext(ctx, `
			SELECT coalesce(max(CASE WHEN amount < 0 THEN id END), 0), coalesce(max(CASE WHEN amount > 0 THEN id END), 0)
			FROM transactions WHERE transfer_id = ?`, transferID).Scan(&debit, &credit)
		if err != nil {
			return err
		}
		if debit == 0 && credit == 0 {
			return ErrNotFound
		}
		if err := UnlinkTransfers(ctx, tx, []int64{transferID}); err != nil {
			return err
		}
		if debit != 0 && credit != 0 {
			_, err = tx.ExecContext(ctx,
				`INSERT OR IGNORE INTO transfer_dismissals (debit_id, credit_id) VALUES (?, ?)`, debit, credit)
		}
		return err
	})
}

// UnlinkTransfers défait des virements dans une transaction en cours : leurs
// opérations restent, sans lien. L'annulation d'un import l'utilise avant de
// supprimer ses opérations, pour ne pas laisser de virement à une seule jambe.
func UnlinkTransfers(ctx context.Context, tx *sql.Tx, transferIDs []int64) error {
	if len(transferIDs) == 0 {
		return nil
	}
	list, err := json.Marshal(transferIDs)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE transactions SET transfer_id = NULL WHERE transfer_id IN (SELECT value FROM json_each(?))`, string(list)); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM transfers WHERE id IN (SELECT value FROM json_each(?))`, string(list))
	return err
}
