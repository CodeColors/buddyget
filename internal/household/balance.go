package household

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"

	"github.com/codecolors/buddyget/internal/ledger"
	"github.com/codecolors/buddyget/internal/platform/calendar"
	"github.com/codecolors/buddyget/internal/platform/db"
	"github.com/codecolors/buddyget/internal/platform/money"
	"github.com/codecolors/buddyget/internal/platform/validation"
)

// Balance est la position d'un membre : ce qu'il a avancé pour le foyer,
// sa part des dépenses partagées, les remboursements faits et reçus.
// Positif, on lui doit ; négatif, il doit.
type Balance struct {
	Member
	Paid    int64 // avancé : dépenses payées, moins les recettes encaissées
	Owed    int64 // sa part, dépenses moins recettes
	Settled int64 // remboursements faits, moins reçus
	Net     int64 // Paid − Owed + Settled
}

// Debt est un remboursement qui solde les comptes entre deux membres.
type Debt struct {
	From, To Member
	Amount   int64
}

// Balances renvoie la position de chaque membre et les remboursements, le
// moins nombreux possible en pratique, qui remettent tout le monde à zéro.
func (s *Service) Balances(ctx context.Context) ([]Balance, []Debt, error) {
	members, err := s.Members(ctx)
	if err != nil {
		return nil, nil, err
	}
	index := make(map[int64]int, len(members))
	out := make([]Balance, len(members))
	for i, m := range members {
		index[m.ID], out[i] = i, Balance{Member: m}
	}
	// Une dépense (montant négatif) avancée par le payeur lui est due par
	// chacun selon sa part ; une recette partagée encaissée par le payeur,
	// l'inverse.
	type move struct {
		member       int64
		paid, shared int64
	}
	moves, err := db.Collect(ctx, s.db.R, func(row db.Scanner) (move, error) {
		var m move
		err := row.Scan(&m.member, &m.paid, &m.shared)
		return m, err
	}, `
		SELECT st.payer_id, -t.amount, 0 FROM shared_transactions st JOIN transactions t ON t.id = st.transaction_id
		UNION ALL
		SELECT sp.member_id, 0, CASE WHEN t.amount < 0 THEN sp.share ELSE -sp.share END
		FROM transaction_splits sp JOIN transactions t ON t.id = sp.transaction_id`)
	if err != nil {
		return nil, nil, err
	}
	for _, m := range moves {
		b := &out[index[m.member]]
		b.Paid += m.paid
		b.Owed += m.shared
	}
	settlements, err := s.Settlements(ctx)
	if err != nil {
		return nil, nil, err
	}
	for _, st := range settlements {
		out[index[st.FromID]].Settled += st.Amount
		out[index[st.ToID]].Settled -= st.Amount
	}
	for i := range out {
		out[i].Net = out[i].Paid - out[i].Owed + out[i].Settled
	}
	return out, settle(out), nil
}

// settle apparie les membres qui doivent à ceux à qui l'on doit, du plus
// fort montant au plus faible.
func settle(balances []Balance) []Debt {
	var debtors, creditors []Balance
	for _, b := range balances {
		switch {
		case b.Net < 0:
			debtors = append(debtors, b)
		case b.Net > 0:
			creditors = append(creditors, b)
		}
	}
	slices.SortStableFunc(debtors, func(a, b Balance) int { return cmp.Compare(a.Net, b.Net) })
	slices.SortStableFunc(creditors, func(a, b Balance) int { return cmp.Compare(b.Net, a.Net) })
	var debts []Debt
	for i, j := 0, 0; i < len(debtors) && j < len(creditors); {
		amount := min(-debtors[i].Net, creditors[j].Net)
		debts = append(debts, Debt{From: debtors[i].Member, To: creditors[j].Member, Amount: amount})
		debtors[i].Net += amount
		creditors[j].Net -= amount
		if debtors[i].Net == 0 {
			i++
		}
		if creditors[j].Net == 0 {
			j++
		}
	}
	return debts
}

// Settlement est un remboursement entre membres.
type Settlement struct {
	ID         int64
	Date       calendar.Date
	FromID     int64
	FromName   string
	ToID       int64
	ToName     string
	Amount     int64
	TransferID int64 // virement interne qui le matérialise, 0 sans comptes suivis
}

// SettlementInput est la saisie d'un remboursement. Avec les deux comptes,
// il est enregistré comme un virement interne de l'un à l'autre.
type SettlementInput struct {
	Date          calendar.Date
	FromID, ToID  int64
	Amount        int64
	FromAccountID int64
	ToAccountID   int64
}

// Settlements renvoie les remboursements, du plus récent au plus ancien.
func (s *Service) Settlements(ctx context.Context) ([]Settlement, error) {
	return db.Collect(ctx, s.db.R, func(row db.Scanner) (Settlement, error) {
		var st Settlement
		err := row.Scan(&st.ID, &st.Date, &st.FromID, &st.FromName, &st.ToID, &st.ToName, &st.Amount, &st.TransferID)
		return st, err
	}, `
		SELECT s.id, s.date, s.from_id, f.name, s.to_id, t.name, s.amount, coalesce(s.transfer_id, 0)
		FROM member_settlements s JOIN household_members f ON f.id = s.from_id JOIN household_members t ON t.id = s.to_id
		ORDER BY s.date DESC, s.id DESC`)
}

// Settle enregistre un remboursement.
func (s *Service) Settle(ctx context.Context, in SettlementInput) (int64, error) {
	switch {
	case in.Date == "":
		return 0, validation.Error("Indiquez la date du remboursement.")
	case in.Amount <= 0 || in.Amount > money.Max:
		return 0, validation.Error("Indiquez le montant remboursé.")
	case in.FromID == in.ToID:
		return 0, validation.Error("Choisissez deux membres différents.")
	case (in.FromAccountID == 0) != (in.ToAccountID == 0):
		return 0, validation.Error("Choisissez les deux comptes, ou aucun.")
	}
	from, err := s.Member(ctx, in.FromID)
	if err != nil {
		return 0, memberError(err)
	}
	to, err := s.Member(ctx, in.ToID)
	if err != nil {
		return 0, memberError(err)
	}
	var transferID int64
	if in.FromAccountID != 0 {
		id, err := s.ledger.CreateTransfer(ctx, ledger.TransferInput{
			FromAccountID: in.FromAccountID, ToAccountID: in.ToAccountID, Date: in.Date, Amount: in.Amount,
			Label: fmt.Sprintf("Remboursement %s → %s", from.Name, to.Name),
		})
		if err != nil {
			return 0, err
		}
		t, err := s.ledger.Transaction(ctx, id)
		if err != nil {
			return 0, err
		}
		transferID = t.TransferID
	}
	res, err := s.db.W.ExecContext(ctx, `INSERT INTO member_settlements (date, from_id, to_id, amount, transfer_id) VALUES (?, ?, ?, ?, nullif(?, 0))`,
		in.Date, in.FromID, in.ToID, in.Amount, transferID)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// DeleteSettlement annule un remboursement, et son virement interne.
func (s *Service) DeleteSettlement(ctx context.Context, id int64) error {
	var leg int64
	err := s.db.R.QueryRowContext(ctx, `
		SELECT coalesce((SELECT min(t.id) FROM transactions t WHERE t.transfer_id = s.transfer_id), 0)
		FROM member_settlements s WHERE s.id = ?`, id).Scan(&leg)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if _, err := s.db.W.ExecContext(ctx, `DELETE FROM member_settlements WHERE id = ?`, id); err != nil {
		return err
	}
	if leg == 0 {
		return nil
	}
	if err := s.ledger.DeleteTransaction(ctx, leg); err != nil && !errors.Is(err, ledger.ErrNotFound) {
		return err
	}
	return nil
}

// memberError transforme un membre introuvable en erreur de saisie.
func memberError(err error) error {
	if errors.Is(err, ErrNotFound) {
		return validation.Error("Choisissez deux membres du foyer.")
	}
	return err
}
