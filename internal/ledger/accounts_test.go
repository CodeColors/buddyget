package ledger

import (
	"errors"
	"testing"
)

func TestAccountBalances(t *testing.T) {
	s := newTestService(t)
	ctx := t.Context()
	courant := mustAccount(t, s, "Courant", 100_00, "2026-10-01")
	livret := mustAccount(t, s, "Épargne", 1000_00, "2026-01-01")

	for _, in := range []TransactionInput{
		{AccountID: courant, Date: "2026-09-30", Label: "Avant le solde initial", Amount: -999_00},
		{AccountID: courant, Date: "2026-10-01", Label: "Jour du solde initial", Amount: -10_00},
		{AccountID: courant, Date: "2026-10-31", Label: "Aujourd'hui", Amount: 25_50},
		{AccountID: courant, Date: "2026-11-15", Label: "Planifiée", Amount: -40_00},
	} {
		mustTx(t, s, in)
	}
	if _, err := s.CreateTransfer(ctx, TransferInput{FromAccountID: courant, ToAccountID: livret, Date: "2026-10-15", Label: "Épargne", Amount: 50_00}); err != nil {
		t.Fatal(err)
	}

	got, err := s.Accounts(ctx, "2026-10-31")
	if err != nil {
		t.Fatal(err)
	}
	// Ordre alphabétique français : « Courant » avant « Épargne ».
	want := []struct {
		name    string
		balance int64
	}{{"Courant", 100_00 - 10_00 + 25_50 - 50_00}, {"Épargne", 1050_00}}
	if len(got) != len(want) {
		t.Fatalf("%d comptes, attendu %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].Name != w.name || got[i].Balance != w.balance {
			t.Errorf("compte %d = %s %d, attendu %s %d", i, got[i].Name, got[i].Balance, w.name, w.balance)
		}
	}

	// Les comptes archivés passent après les comptes actifs.
	if err := s.SetAccountArchived(ctx, courant, true); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Accounts(ctx, "2026-10-31")
	if got[0].Name != "Épargne" || !got[1].Archived {
		t.Errorf("ordre après archivage : %s, %s", got[0].Name, got[1].Name)
	}
}

func TestAccountValidation(t *testing.T) {
	s := newTestService(t)
	ctx := t.Context()
	mustAccount(t, s, "Joint", 0, "2026-10-01")
	base := AccountInput{Name: "Perso", Kind: AccountCurrent, Bank: BankBNP, OpeningDate: "2026-10-01"}

	for name, c := range map[string]struct {
		mutate func(*AccountInput)
		msg    string
	}{
		"nom vide":        {func(in *AccountInput) { in.Name = "  " }, "nom du compte"},
		"nom en double":   {func(in *AccountInput) { in.Name = "joint" }, "déjà ce nom"},
		"type inconnu":    {func(in *AccountInput) { in.Kind = "pel" }, "type de compte"},
		"banque inconnue": {func(in *AccountInput) { in.Bank = "LCL" }, "banque"},
		"IBAN invalide":   {func(in *AccountInput) { in.IBAN = "FR76-12" }, "IBAN"},
		"date manquante":  {func(in *AccountInput) { in.OpeningDate = "" }, "date"},
	} {
		t.Run(name, func(t *testing.T) {
			in := base
			c.mutate(&in)
			_, err := s.CreateAccount(ctx, in)
			expectInputError(t, err, c.msg)
		})
	}
}

func TestAccountIBANIsMasked(t *testing.T) {
	s := newTestService(t)
	ctx := t.Context()
	id, err := s.CreateAccount(ctx, AccountInput{
		Name: "Perso", Kind: AccountCurrent, Bank: BankBNP, OpeningDate: "2026-10-01",
		IBAN: "fr76 3000 4000 0312 3456 7890 143",
	})
	if err != nil {
		t.Fatal(err)
	}
	a, _ := s.Account(ctx, id)
	if a.IBAN != "FR76 •••• 0143" {
		t.Fatalf("IBAN stocké = %q", a.IBAN)
	}

	// Renvoyer la valeur masquée la conserve ; une valeur vide l'efface.
	in := AccountInput{Name: "Perso", Kind: AccountCurrent, Bank: BankBNP, OpeningDate: "2026-10-01", IBAN: a.IBAN}
	if err := s.UpdateAccount(ctx, id, in); err != nil {
		t.Fatal(err)
	}
	if a, _ = s.Account(ctx, id); a.IBAN != "FR76 •••• 0143" {
		t.Errorf("IBAN modifié par erreur : %q", a.IBAN)
	}
	in.IBAN = ""
	if err := s.UpdateAccount(ctx, id, in); err != nil {
		t.Fatal(err)
	}
	if a, _ = s.Account(ctx, id); a.IBAN != "" {
		t.Errorf("IBAN non effacé : %q", a.IBAN)
	}
}

func TestDeleteAccount(t *testing.T) {
	s := newTestService(t)
	ctx := t.Context()
	vide := mustAccount(t, s, "Vide", 0, "2026-10-01")
	plein := mustAccount(t, s, "Plein", 0, "2026-10-01")
	mustTx(t, s, TransactionInput{AccountID: plein, Date: "2026-10-02", Label: "Café", Amount: -2_00})

	if err := s.DeleteAccount(ctx, plein); err == nil {
		t.Error("un compte avec des opérations ne doit pas être supprimé")
	} else {
		expectInputError(t, err, "archivez-le")
	}
	if err := s.DeleteAccount(ctx, vide); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Account(ctx, vide); !errors.Is(err, ErrNotFound) {
		t.Errorf("compte supprimé encore présent : %v", err)
	}
	if err := s.DeleteAccount(ctx, vide); !errors.Is(err, ErrNotFound) {
		t.Errorf("seconde suppression : %v, attendu ErrNotFound", err)
	}
}
