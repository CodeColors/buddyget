package household

import (
	"errors"
	"path/filepath"
	"slices"
	"testing"

	"github.com/codecolors/buddyget/internal/ledger"
	"github.com/codecolors/buddyget/internal/platform/db"
	"github.com/codecolors/buddyget/internal/platform/validation"
)

func amounts(shares []Share) []int64 {
	out := make([]int64, len(shares))
	for i, s := range shares {
		out[i] = s.Amount
	}
	return out
}

func TestAllocate(t *testing.T) {
	hugo, lea, sam := Member{ID: 1, Name: "Hugo", Income: 3_000_00}, Member{ID: 2, Name: "Léa", Income: 2_000_00}, Member{ID: 3, Name: "Sam"}
	for _, c := range []struct {
		total   int64
		mode    Mode
		members []Member
		free    map[int64]int64
		want    []int64
	}{
		{100_00, Equal, []Member{hugo, lea}, nil, []int64{50_00, 50_00}},
		{100_00, Equal, []Member{hugo, lea, sam}, nil, []int64{33_34, 33_33, 33_33}}, // centime restant au premier
		{100_00, Prorata, []Member{hugo, lea}, nil, []int64{60_00, 40_00}},
		{1_00, Prorata, []Member{hugo, lea, sam}, nil, []int64{60, 40, 0}},
		{33_33, Prorata, []Member{hugo, lea}, nil, []int64{20_00, 13_33}}, // plus fort reste
		{100_00, Free, []Member{hugo, lea}, map[int64]int64{1: 70_00, 2: 30_00}, []int64{70_00, 30_00}},
		// Montants extrêmes : produits calculés sans dépassement.
		{99_999_999_999, Prorata, []Member{{ID: 1, Income: 99_999_999_999}, {ID: 2, Income: 1}}, nil, []int64{99_999_999_998, 1}},
	} {
		shares, err := Allocate(c.total, c.mode, c.members, c.free)
		if got := amounts(shares); err != nil || !slices.Equal(got, c.want) {
			t.Errorf("%d en %s : %v, %v (attendu %v)", c.total, c.mode, got, err, c.want)
		}
	}
	for _, c := range []struct {
		mode    Mode
		members []Member
		free    map[int64]int64
	}{
		{Equal, nil, nil},
		{Prorata, []Member{sam}, nil},
		{Free, []Member{hugo, lea}, map[int64]int64{1: 70_00, 2: 20_00}},
		{Free, []Member{hugo, lea}, map[int64]int64{1: 110_00, 2: -10_00}},
		{"autre", []Member{hugo}, nil},
	} {
		if _, err := Allocate(100_00, c.mode, c.members, c.free); !isInput(err) {
			t.Errorf("répartition invalide acceptée : %s %v %v (%v)", c.mode, c.members, c.free, err)
		}
	}
}

type env struct {
	s       *Service
	ledger  *ledger.Service
	account int64
}

func newEnv(t *testing.T) env {
	d, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "household.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	l := ledger.NewService(d)
	account, err := l.CreateAccount(t.Context(), ledger.AccountInput{Name: "Joint", Kind: ledger.AccountCurrent, Bank: ledger.BankSG, OpeningDate: "2026-01-01"})
	if err != nil {
		t.Fatal(err)
	}
	return env{s: NewService(d, l), ledger: l, account: account}
}

func (e env) op(t *testing.T, label string, amount int64) int64 {
	t.Helper()
	id, err := e.ledger.CreateTransaction(t.Context(), ledger.TransactionInput{AccountID: e.account, Date: "2026-10-15", Label: label, Amount: amount})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestMembers(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	for _, bad := range []MemberInput{{Name: " "}, {Name: "Hugo", Income: -1}} {
		if _, err := e.s.CreateMember(ctx, bad); !isInput(err) {
			t.Errorf("membre invalide accepté : %+v", bad)
		}
	}
	hugo, err := e.s.CreateMember(ctx, MemberInput{Name: "Hugo", Income: 3_000_00})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.CreateMember(ctx, MemberInput{Name: "hugo"}); !isInput(err) {
		t.Error("nom en double accepté")
	}
	lea, _ := e.s.CreateMember(ctx, MemberInput{Name: "Léa"})
	if err := e.s.UpdateMember(ctx, lea, MemberInput{Name: "Léa", Income: 2_000_00}); err != nil {
		t.Fatal(err)
	}
	if err := e.s.SetArchived(ctx, hugo, true); err != nil {
		t.Fatal(err)
	}
	if list, _ := e.s.Members(ctx); len(list) != 2 || list[0].Name != "Léa" || list[0].Income != 2_000_00 || !list[1].Archived {
		t.Errorf("membres : %+v", list)
	}
	// Un membre présent dans un partage ne se supprime pas.
	if err := e.s.SetSplit(ctx, e.op(t, "Courses", -50_00), Split{PayerID: lea, Mode: Equal, Members: []int64{hugo, lea}}); err != nil {
		t.Fatal(err)
	}
	if err := e.s.DeleteMember(ctx, hugo); !isInput(err) {
		t.Errorf("membre utilisé supprimé : %v", err)
	}
	other, _ := e.s.CreateMember(ctx, MemberInput{Name: "Sam"})
	if err := e.s.DeleteMember(ctx, other); err != nil {
		t.Fatal(err)
	}
	if err := e.s.UpdateMember(ctx, other, MemberInput{Name: "Sam"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("membre supprimé : %v", err)
	}
}

func TestSharingAndSettling(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	hugo, _ := e.s.CreateMember(ctx, MemberInput{Name: "Hugo", Income: 3_000_00})
	lea, _ := e.s.CreateMember(ctx, MemberInput{Name: "Léa", Income: 2_000_00})
	both := []int64{hugo, lea}

	// Hugo avance 100 € de courses (50/50) et 500 € de loyer (au prorata) ;
	// Léa avance 30 € de restaurant (montants libres) et encaisse un
	// remboursement d'assurance de 20 € (50/50).
	shares := []struct {
		label  string
		amount int64
		split  Split
	}{
		{"Courses", -100_00, Split{PayerID: hugo, Mode: Equal, Members: both}},
		{"Loyer", -500_00, Split{PayerID: hugo, Mode: Prorata, Members: both}},
		{"Restaurant", -30_00, Split{PayerID: lea, Mode: Free, Members: both, Amounts: map[int64]int64{hugo: 20_00, lea: 10_00}}},
		{"Remboursement assurance", 20_00, Split{PayerID: lea, Mode: Equal, Members: both}},
	}
	for _, sh := range shares {
		if err := e.s.SetSplit(ctx, e.op(t, sh.label, sh.amount), sh.split); err != nil {
			t.Fatalf("%s : %v", sh.label, err)
		}
	}
	balances, debts, err := e.s.Balances(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Hugo : avancé 600, part 50 + 300 + 20 − 10 = 360 → +240 ; Léa : −240.
	if balances[0].Name != "Hugo" || balances[0].Paid != 600_00 || balances[0].Owed != 360_00 || balances[0].Net != 240_00 || balances[1].Net != -240_00 {
		t.Fatalf("soldes : %+v", balances)
	}
	if len(debts) != 1 || debts[0].From.Name != "Léa" || debts[0].To.Name != "Hugo" || debts[0].Amount != 240_00 {
		t.Fatalf("dettes : %+v", debts)
	}

	// Le partage suit le montant ; il se retire.
	recent, err := e.s.SharedTransactions(ctx, 10)
	if err != nil || len(recent) != 4 {
		t.Fatalf("opérations partagées : %+v, %v", recent, err)
	}
	courses := recent[len(recent)-1].TransactionID
	if sh, ok, _ := e.s.Split(ctx, courses); !ok || sh.PayerName != "Hugo" || len(sh.Shares) != 2 || sh.Shares[0].Amount != 50_00 {
		t.Errorf("partage des courses : %+v", sh)
	}
	if err := e.s.ClearSplit(ctx, courses); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := e.s.Split(ctx, courses); ok {
		t.Error("partage retiré")
	}
	if err := e.s.SetSplit(ctx, 999, shares[0].split); !errors.Is(err, ErrNotFound) {
		t.Errorf("opération inconnue : %v", err)
	}
	if err := e.s.SetSplit(ctx, courses, Split{PayerID: 999, Mode: Equal, Members: both}); !isInput(err) {
		t.Errorf("payeur inconnu : %v", err)
	}

	// Sans les courses, Léa doit 190 € à Hugo ; elle les rembourse entre deux
	// comptes suivis : un virement interne.
	perso, _ := e.ledger.CreateAccount(ctx, ledger.AccountInput{Name: "Perso Léa", Kind: ledger.AccountCurrent, Bank: ledger.BankBNP, OpeningDate: "2026-01-01"})
	for _, bad := range []SettlementInput{
		{Date: "2026-10-20", FromID: lea, ToID: lea, Amount: 1},
		{Date: "2026-10-20", FromID: lea, ToID: hugo},
		{Date: "2026-10-20", FromID: lea, ToID: hugo, Amount: 1, FromAccountID: perso},
		{Date: "2026-10-20", FromID: lea, ToID: 999, Amount: 1},
	} {
		if _, err := e.s.Settle(ctx, bad); !isInput(err) {
			t.Errorf("remboursement invalide accepté : %+v (%v)", bad, err)
		}
	}
	id, err := e.s.Settle(ctx, SettlementInput{Date: "2026-10-20", FromID: lea, ToID: hugo, Amount: 190_00, FromAccountID: perso, ToAccountID: e.account})
	if err != nil {
		t.Fatal(err)
	}
	if _, debts, _ = e.s.Balances(ctx); len(debts) != 0 {
		t.Errorf("tout est remboursé : %+v", debts)
	}
	list, _ := e.s.Settlements(ctx)
	if len(list) != 1 || list[0].TransferID == 0 || list[0].FromName != "Léa" {
		t.Fatalf("remboursements : %+v", list)
	}
	page, _ := e.ledger.Search(ctx, ledger.Filter{Query: "Remboursement Léa"}, 0)
	if page.Count != 2 {
		t.Errorf("virement interne du remboursement : %d jambes", page.Count)
	}
	if err := e.s.DeleteSettlement(ctx, id); err != nil {
		t.Fatal(err)
	}
	if page, _ := e.ledger.Search(ctx, ledger.Filter{Query: "Remboursement Léa"}, 0); page.Count != 0 {
		t.Error("le virement disparaît avec le remboursement")
	}
	if err := e.s.DeleteSettlement(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Errorf("remboursement supprimé : %v", err)
	}
	// Sans comptes : un simple remboursement.
	if _, err := e.s.Settle(ctx, SettlementInput{Date: "2026-10-21", FromID: lea, ToID: hugo, Amount: 50_00}); err != nil {
		t.Fatal(err)
	}
	if balances, debts, _ := e.s.Balances(ctx); balances[1].Settled != 50_00 || len(debts) != 1 || debts[0].Amount != 140_00 {
		t.Errorf("après un remboursement en espèces : %+v, %+v", balances, debts)
	}
}

func TestSettleManyMembers(t *testing.T) {
	m := func(id int64, net int64) Balance { return Balance{Member: Member{ID: id}, Net: net} }
	debts := settle([]Balance{m(1, 70), m(2, -50), m(3, 30), m(4, -40), m(5, -10)})
	var total int64
	for _, d := range debts {
		total += d.Amount
	}
	if total != 100 || len(debts) > 4 || debts[0].From.ID != 2 || debts[0].To.ID != 1 {
		t.Errorf("remboursements : %+v", debts)
	}
}

func isInput(err error) bool {
	_, ok := validation.Message(err)
	return ok
}
