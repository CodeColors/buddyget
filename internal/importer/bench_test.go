package importer

import (
	"bytes"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/codecolors/buddyget/internal/categorize"
	"github.com/codecolors/buddyget/internal/ledger"
	"github.com/codecolors/buddyget/internal/platform/db"
)

// benchCSV produit un export de n lignes au format Société Générale.
func benchCSV(n int) []byte {
	var b bytes.Buffer
	b.WriteString("Date de l'opération;Libellé;Détail de l'écriture;Montant de l'opération;Devise\n")
	labels := []string{"CARTE X4321 BOULANGERIE", "PRLV SEPA EDF", "CARTE X4321 CAFÉ DE LA GARE", "VIR RECU SALAIRE"}
	for i := range n {
		fmt.Fprintf(&b, "%02d/%02d/2026;%s;%s %d;-%d,%02d;EUR\n", i%28+1, i%12+1, labels[i%4], labels[i%4], i%50, i%300+1, i%100)
	}
	return b.Bytes()
}

// BenchmarkImport10000 dépose puis valide un relevé de 10 000 lignes.
func BenchmarkImport10000(b *testing.B) {
	data := benchCSV(10_000)
	d, err := db.Open(b.Context(), filepath.Join(b.TempDir(), "bench.db"))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = d.Close() })
	l := ledger.NewService(d)
	s := NewService(d, b.TempDir(), 10<<20, categorize.NewService(d))
	i := 0
	for b.Loop() {
		i++
		account, err := l.CreateAccount(b.Context(), ledger.AccountInput{
			Name: fmt.Sprintf("Compte %d", i), Kind: ledger.AccountCurrent, Bank: ledger.BankSG, OpeningDate: "2026-01-01",
		})
		if err != nil {
			b.Fatal(err)
		}
		token, err := s.Upload(b.Context(), account, "releve.csv", bytes.NewReader(data))
		if err != nil {
			b.Fatal(err)
		}
		imp, err := s.Commit(b.Context(), token)
		if err != nil || imp.Added != 10_000 {
			b.Fatalf("%+v, %v", imp, err)
		}
	}
}
