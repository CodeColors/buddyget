package text

import "testing"

func TestFold(t *testing.T) {
	for in, want := range map[string]string{
		"Détail de l’écriture":      "detail de l'ecriture",
		"  ÉPARGNE   Œuvre\tCAFÉ  ": "epargne oeuvre cafe",
		"Noël à Saint-Étienne":      "noel a saint-etienne",
		"":                          "",
		"Montant (€)":               "montant (€)",
	} {
		if got := Fold(in); got != want {
			t.Errorf("Fold(%q) = %q, attendu %q", in, got, want)
		}
	}
}
