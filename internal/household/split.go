package household

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"math/bits"
	"slices"

	"github.com/codecolors/buddyget/internal/ledger"
	"github.com/codecolors/buddyget/internal/platform/calendar"
	"github.com/codecolors/buddyget/internal/platform/db"
	"github.com/codecolors/buddyget/internal/platform/money"
	"github.com/codecolors/buddyget/internal/platform/validation"
)

// Mode est la règle de répartition d'une opération partagée.
type Mode string

const (
	Equal   Mode = "egal"    // parts égales
	Prorata Mode = "prorata" // au prorata des revenus des membres
	Free    Mode = "libre"   // montants saisis
)

// Modes sont les règles proposées, dans l'ordre d'affichage.
var Modes = []Mode{Equal, Prorata, Free}

// Label décrit une règle de répartition.
func (m Mode) Label() string {
	switch m {
	case Equal:
		return "Parts égales"
	case Prorata:
		return "Au prorata"
	}
	return "Montants libres"
}

// Split est la saisie d'un partage : le payeur, la règle et les membres
// concernés ; Amounts donne la part de chacun en montants libres.
type Split struct {
	PayerID int64
	Mode    Mode
	Members []int64
	Amounts map[int64]int64
}

// Share est la part d'un membre, en valeur absolue.
type Share struct {
	MemberID   int64
	MemberName string
	Amount     int64
}

// Allocate répartit total (positif) entre les membres selon la règle. Les
// centimes restants vont aux premiers membres, dans l'ordre donné : la
// somme des parts vaut toujours total.
func Allocate(total int64, mode Mode, members []Member, amounts map[int64]int64) ([]Share, error) {
	if len(members) == 0 {
		return nil, validation.Error("Choisissez les membres qui partagent l'opération.")
	}
	shares := make([]Share, len(members))
	for i, m := range members {
		shares[i] = Share{MemberID: m.ID, MemberName: m.Name}
	}
	switch mode {
	case Equal:
		weights := make([]int64, len(members))
		for i := range weights {
			weights[i] = 1
		}
		spread(total, weights, shares)
	case Prorata:
		weights := make([]int64, len(members))
		for i, m := range members {
			weights[i] = m.Income
		}
		if slices.Max(weights) == 0 {
			return nil, validation.Error("Indiquez le revenu des membres pour répartir au prorata.")
		}
		spread(total, weights, shares)
	case Free:
		var sum int64
		for i, m := range members {
			a := amounts[m.ID]
			if a < 0 {
				return nil, validation.Error("Une part ne peut pas être négative.")
			}
			shares[i].Amount, sum = a, sum+a
		}
		if sum != total {
			return nil, validation.Errorf("Les parts totalisent %s au lieu de %s.", money.Format(sum), money.Format(total))
		}
	default:
		return nil, validation.Error("Choisissez la règle de répartition.")
	}
	return shares, nil
}

// spread répartit total au prorata des poids, positifs ou nuls dont un au
// moins est positif (méthode du plus fort reste, départagée par l'ordre des
// membres). Les produits total × poids sont calculés sur 128 bits.
func spread(total int64, weights []int64, shares []Share) {
	var sum uint64
	for _, w := range weights {
		sum += uint64(w) //nolint:gosec // G115 : poids positifs ou nuls.
	}
	rest := total
	remainders := make([]uint64, len(weights))
	for i, w := range weights {
		hi, lo := bits.Mul64(uint64(total), uint64(w)) //nolint:gosec // G115 : montant et poids positifs.
		q, r := bits.Div64(hi, lo, sum)                // quotient ≤ total : pas de dépassement
		shares[i].Amount, remainders[i] = int64(q), r  //nolint:gosec // G115 : q ≤ total.
		rest -= shares[i].Amount
	}
	order := make([]int, len(weights))
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(a, b int) int { return cmp.Compare(remainders[b], remainders[a]) })
	for _, i := range order[:rest] {
		shares[i].Amount++
	}
}

// Plan calcule les parts d'une opération de montant amount (signé) avant
// son enregistrement : la saisie fautive est refusée avant toute écriture.
func (s *Service) Plan(ctx context.Context, amount int64, sp Split) ([]Share, error) {
	members, err := s.Members(ctx)
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]Member, len(members))
	for _, m := range members {
		byID[m.ID] = m
	}
	if _, ok := byID[sp.PayerID]; !ok {
		return nil, validation.Error("Choisissez qui a payé.")
	}
	if amount == 0 {
		return nil, validation.Error("Une opération sans montant ne se partage pas.")
	}
	var chosen []Member
	for _, id := range sp.Members {
		m, ok := byID[id]
		if !ok {
			return nil, validation.Error("Membre introuvable.")
		}
		if !slices.ContainsFunc(chosen, func(c Member) bool { return c.ID == id }) {
			chosen = append(chosen, m)
		}
	}
	return Allocate(max(amount, -amount), sp.Mode, chosen, sp.Amounts)
}

// SetSplit enregistre (ou remplace) le partage d'une opération.
func (s *Service) SetSplit(ctx context.Context, transactionID int64, sp Split) error {
	t, err := s.ledger.Transaction(ctx, transactionID)
	if errors.Is(err, ledger.ErrNotFound) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if t.IsTransfer() {
		return validation.Error("Un virement entre vos comptes ne se partage pas.")
	}
	shares, err := s.Plan(ctx, t.Amount, sp)
	if err != nil {
		return err
	}
	return s.db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO shared_transactions (transaction_id, payer_id, mode) VALUES (?, ?, ?)
			ON CONFLICT (transaction_id) DO UPDATE SET payer_id = excluded.payer_id, mode = excluded.mode`,
			transactionID, sp.PayerID, sp.Mode); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM transaction_splits WHERE transaction_id = ?`, transactionID); err != nil {
			return err
		}
		for _, sh := range shares {
			if _, err := tx.ExecContext(ctx, `INSERT INTO transaction_splits (transaction_id, member_id, share) VALUES (?, ?, ?)`,
				transactionID, sh.MemberID, sh.Amount); err != nil {
				return err
			}
		}
		return nil
	})
}

// ClearSplit retire le partage d'une opération (sans effet si elle n'en a
// pas).
func (s *Service) ClearSplit(ctx context.Context, transactionID int64) error {
	_, err := s.db.W.ExecContext(ctx, `DELETE FROM shared_transactions WHERE transaction_id = ?`, transactionID)
	return err
}

// Shared est une opération partagée.
type Shared struct {
	TransactionID int64
	Date          calendar.Date
	Label         string
	Amount        int64
	AccountName   string
	PayerID       int64
	PayerName     string
	Mode          Mode
	Shares        []Share
}

// Split renvoie le partage d'une opération ; faux si elle n'est pas
// partagée.
func (s *Service) Split(ctx context.Context, transactionID int64) (Shared, bool, error) {
	list, err := s.shared(ctx, `WHERE st.transaction_id = ?`, transactionID)
	if err != nil || len(list) == 0 {
		return Shared{}, false, err
	}
	return list[0], true, nil
}

// SharedTransactions renvoie les dernières opérations partagées.
func (s *Service) SharedTransactions(ctx context.Context, limit int) ([]Shared, error) {
	return s.shared(ctx, `ORDER BY t.date DESC, t.id DESC LIMIT ?`, limit)
}

func (s *Service) shared(ctx context.Context, clause string, args ...any) ([]Shared, error) {
	list, err := db.Collect(ctx, s.db.R, func(row db.Scanner) (Shared, error) {
		var sh Shared
		err := row.Scan(&sh.TransactionID, &sh.Date, &sh.Label, &sh.Amount, &sh.AccountName, &sh.PayerID, &sh.PayerName, &sh.Mode)
		return sh, err
	}, `
		SELECT t.id, t.date, t.label, t.amount, a.name, st.payer_id, m.name, st.mode
		FROM shared_transactions st
		JOIN transactions t ON t.id = st.transaction_id
		JOIN accounts a ON a.id = t.account_id
		JOIN household_members m ON m.id = st.payer_id `+clause, args...)
	if err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].Shares, err = s.shares(ctx, list[i].TransactionID); err != nil {
			return nil, err
		}
	}
	return list, nil
}

func (s *Service) shares(ctx context.Context, transactionID int64) ([]Share, error) {
	return db.Collect(ctx, s.db.R, func(row db.Scanner) (Share, error) {
		var sh Share
		err := row.Scan(&sh.MemberID, &sh.MemberName, &sh.Amount)
		return sh, err
	}, `
		SELECT s.member_id, m.name, s.share FROM transaction_splits s JOIN household_members m ON m.id = s.member_id
		WHERE s.transaction_id = ? ORDER BY m.name COLLATE NOCASE`, transactionID)
}
