package ledger

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/codecolors/buddyget/internal/platform/calendar"
	"github.com/codecolors/buddyget/internal/platform/db"
	"github.com/codecolors/buddyget/internal/platform/validation"
)

func newTestService(t testing.TB) *Service {
	t.Helper()
	d, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "ledger.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return NewService(d)
}

func mustAccount(t testing.TB, s *Service, name string, opening int64, date calendar.Date) int64 {
	t.Helper()
	id, err := s.CreateAccount(t.Context(), AccountInput{
		Name: name, Kind: AccountCurrent, Bank: BankSG, OpeningBalance: opening, OpeningDate: date,
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func mustTx(t *testing.T, s *Service, in TransactionInput) int64 {
	t.Helper()
	id, err := s.CreateTransaction(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// categoryID retrouve une catégorie par défaut par son nom complet.
func categoryID(t *testing.T, s *Service, fullName string) int64 {
	t.Helper()
	cats, err := s.Categories(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cats {
		if c.FullName() == fullName {
			return c.ID
		}
	}
	t.Fatalf("catégorie %q introuvable", fullName)
	return 0
}

func expectInputError(t *testing.T, err error, contains string) {
	t.Helper()
	msg, ok := validation.Message(err)
	if !ok {
		t.Fatalf("erreur de saisie attendue (%q), obtenu %v", contains, err)
	}
	if !strings.Contains(msg, contains) {
		t.Errorf("message %q, attendu %q", msg, contains)
	}
}
