package importer

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/codecolors/buddyget/internal/platform/validation"
)

// headOf renvoie les premières lignes d'un fichier, copiées.
func headOf(t *testing.T, path string) (Format, []HeadRow) {
	t.Helper()
	format, err := sniffFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var head []HeadRow
	err = readRows(path, format, func(line int, cells []string) error {
		head = append(head, HeadRow{line, slices.Clone(cells)})
		if len(head) == detectLines {
			return errStop
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return format, head
}

func TestDetectProfiles(t *testing.T) {
	profiles := Profiles(t.TempDir())
	for _, c := range []struct {
		file    string
		profile string
		header  int
	}{
		{"../../testdata/sg/releve-octobre.csv", "Société Générale", 3},
		{"../../testdata/bnp/releve-octobre.xlsx", "BNP Paribas", 2},
		{"../../testdata/generique/debit-credit.xls", "Générique", 1},
		{"../../testdata/generique/sans-entete.csv", "", 0},
	} {
		t.Run(filepath.Base(c.file), func(t *testing.T) {
			_, head := headOf(t, c.file)
			p, m, ok := detect(profiles, head)
			if p.Name != c.profile || ok != (c.profile != "") || m.HeaderLine != c.header {
				t.Errorf("profil %q (ligne %d), attendu %q (ligne %d)", p.Name, m.HeaderLine, c.profile, c.header)
			}
		})
	}
}

// parseFile applique le profil détecté à tout le fichier.
func parseFile(t *testing.T, path string) []Line {
	t.Helper()
	format, head := headOf(t, path)
	_, m, ok := detect(Profiles(t.TempDir()), head)
	if !ok {
		t.Fatal("aucun profil reconnu")
	}
	var lines []Line
	err := readRows(path, format, func(n int, cells []string) error {
		if n > m.HeaderLine {
			if l, ok := m.parse(n, cells, format != FormatCSV); ok {
				lines = append(lines, l)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return lines
}

func TestParseFixtures(t *testing.T) {
	sg := parseFile(t, "../../testdata/sg/releve-octobre.csv")
	want := []Line{
		{4, "2026-10-31", "CARTE X4321 30/10 BOULANGERIE MARTIN 75 PARIS", -420, ""},
		{5, "2026-10-31", "CARTE X4321 30/10 BOULANGERIE MARTIN 75 PARIS", -420, ""},
		{6, "2026-10-30", "PRLV SEPA EDF CLIENTS PARTICULIERS ECH/301026", -7832, ""},
		{7, "2026-10-29", "VIR RECU 1234567 DE: SOCIETE EXEMPLE MOTIF: SALAIRE OCTOBRE", 245000, ""},
		{8, "2026-10-28", "CARTE X4321 27/10 CAFÉ DE LA GARE 69 LYON", -350, ""},
		{9, "2026-10-27", "CHEQUE 0001234", -12000, ""}, // détail vide : libellé court
	}
	if !slices.Equal(sg, want) {
		t.Errorf("SG :\n obtenu  %v\n attendu %v", sg, want)
	}

	bnp := parseFile(t, "../../testdata/bnp/releve-octobre.xlsx")
	if len(bnp) != 4 || bnp[0] != (Line{3, "2026-10-31", "CB CARREFOUR MARKET 30/10", -8215, ""}) || bnp[2].Amount != 231045 {
		t.Errorf("BNP : %v", bnp)
	}

	gen := parseFile(t, "../../testdata/generique/debit-credit.xls")
	if len(gen) != 3 || gen[0].Amount != -4000 || gen[1].Amount != 2350 || gen[1].Date != "2026-10-16" {
		t.Errorf("débit/crédit : %v", gen)
	}
}

func TestParseLine(t *testing.T) {
	m := Mapping{Date: 0, Labels: []int{1}, Amount: 2, Debit: NoColumn, Credit: NoColumn}
	for name, c := range map[string]struct {
		m           Mapping
		cells       []string
		spreadsheet bool
		want        Line
	}{
		"date avec heure":       {m, []string{"31/10/2026 00:00:00", "Pain", "-1,10"}, false, Line{Date: "2026-10-31", Label: "Pain", Amount: -110}},
		"format imposé":         {Mapping{Date: 0, Labels: []int{1}, Amount: 2, DateFormat: "AAAA-MM-JJ"}, []string{"2026-10-31", "Pain", "-1"}, false, Line{Date: "2026-10-31", Label: "Pain", Amount: -100}},
		"montants inversés":     {Mapping{Date: 0, Labels: []int{1}, Amount: 2, Invert: true}, []string{"31/10/2026", "Pain", "1,10"}, false, Line{Date: "2026-10-31", Label: "Pain", Amount: -110}},
		"flottant de classeur":  {m, []string{"46326", "Pain", "-12.299999999999999"}, true, Line{Date: "2026-10-31", Label: "Pain", Amount: -1230}},
		"espaces du libellé":    {m, []string{"31/10/2026", "  PRLV   SEPA  ", "-1"}, false, Line{Date: "2026-10-31", Label: "PRLV SEPA", Amount: -100}},
		"date illisible":        {m, []string{"Solde au", "", "1 234,56"}, false, Line{Err: "Date illisible « Solde au »."}},
		"libellé absent":        {m, []string{"31/10/2026", "", "-1"}, false, Line{Date: "2026-10-31", Err: "Libellé absent."}},
		"montant absent":        {m, []string{"31/10/2026", "Pain"}, false, Line{Date: "2026-10-31", Label: "Pain", Err: "Montant absent."}},
		"montant ambigu en CSV": {m, []string{"31/10/2026", "Pain", "12.345"}, false, Line{Date: "2026-10-31", Label: "Pain", Err: "Montant invalide : deux décimales au maximum."}},
		"montant nul":           {m, []string{"31/10/2026", "Pain", "0,00"}, false, Line{Date: "2026-10-31", Label: "Pain", Err: "Montant nul."}},
	} {
		t.Run(name, func(t *testing.T) {
			got, ok := c.m.parse(7, c.cells, c.spreadsheet)
			c.want.Number = 7
			if !ok || got != c.want {
				t.Errorf("parse = %+v, %v\n attendu   %+v", got, ok, c.want)
			}
		})
	}
	if _, ok := m.parse(1, []string{"", "  "}, false); ok {
		t.Error("une ligne vide doit être ignorée")
	}
}

func TestDedupHash(t *testing.T) {
	a := Line{Date: "2026-10-31", Label: "Café  de la GARE", Amount: -350}
	b := Line{Date: "2026-10-31", Label: "CAFE DE LA GARE", Amount: -350}
	if dedupKey(a) != dedupKey(b) {
		t.Error("casse, accents et espaces ne doivent pas distinguer deux opérations")
	}
	h := lineHash(1, dedupKey(a), 1)
	for name, other := range map[string]string{
		"rang":    lineHash(1, dedupKey(a), 2),
		"compte":  lineHash(2, dedupKey(a), 1),
		"montant": lineHash(1, dedupKey(Line{Date: a.Date, Label: a.Label, Amount: -351}), 1),
	} {
		if other == h {
			t.Errorf("un %s différent doit changer l'empreinte", name)
		}
	}
	if len(h) != 32 || h != lineHash(1, dedupKey(b), 1) {
		t.Errorf("empreinte instable : %s", h)
	}
}

func TestProfilesOverrideAndCustom(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"sg.yaml":            "nom: SG ajusté\ncolonnes:\n  date: [Jour]\n  libelle: [Texte]\n  montant: [Somme]\n",
		"ma-banque.yaml":     "nom: Ma banque\ncolonnes:\n  date: [Le]\n  libelle: [Quoi]\n  montant: [Combien]\n",
		"cassé.yaml":         "nom: [pas une chaîne\n",
		"incomplet.yaml":     "nom: Sans colonnes\n",
		"mauvaise-date.yaml": "nom: X\ncolonnes:\n  date: [D]\n  libelle: [L]\nformat_date: MM/JJ\n",
		"note.txt":           "ignoré",
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var names []string
	for _, p := range Profiles(dir) {
		names = append(names, p.Name)
	}
	if want := []string{"Ma banque", "SG ajusté", "BNP Paribas", "Générique"}; !slices.Equal(names, want) {
		t.Errorf("profils %v, attendu %v", names, want)
	}
}

func TestSaveProfile(t *testing.T) {
	dir := t.TempDir()
	header := []string{"Jour", "Texte", "", "Débit", "Crédit"}
	m := Mapping{HeaderLine: 1, Date: 0, Labels: []int{1}, Amount: NoColumn, Debit: 3, Credit: 4, DateFormat: "JJ/MM/AA"}
	p, err := SaveProfile(dir, "  Crédit Mutuel  ", header, m)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "credit-mutuel.yaml")); err != nil {
		t.Fatalf("fichier du profil absent : %v", err)
	}
	found, got, ok := detect(Profiles(dir), []HeadRow{{1, []string{"titre"}}, {2, header}})
	if !ok || found.Name != p.Name || got.HeaderLine != 2 || got.Debit != 3 || got.Credit != 4 || got.DateFormat != "JJ/MM/AA" {
		t.Errorf("profil enregistré non reconnu : %+v %+v", found, got)
	}

	for name, c := range map[string]struct {
		name string
		m    Mapping
	}{
		"nom vide":              {" ", m},
		"sans ligne d'en-tête":  {"X", Mapping{Date: 0, Labels: []int{1}, Amount: 2}},
		"colonne sans intitulé": {"X", Mapping{HeaderLine: 1, Date: 0, Labels: []int{2}, Amount: 3}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := SaveProfile(dir, c.name, header, c.m)
			expectInputError(t, err)
		})
	}
}

func TestSlug(t *testing.T) {
	for in, want := range map[string]string{"Crédit Agricole": "credit-agricole", "  La Banque Postale (pro) ": "la-banque-postale-pro", "€€": ""} {
		if got := slug(in); got != want {
			t.Errorf("slug(%q) = %q, attendu %q", in, got, want)
		}
	}
}

func TestDateFormatsAreValid(t *testing.T) {
	day := time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC)
	for _, f := range DateFormats {
		d, err := parseDate(day.Format(f.Layout), f.Label, false)
		if err != nil || d != "2026-10-31" {
			t.Errorf("format %s : %q, %v", f.Label, d, err)
		}
	}
}

func expectInputError(t *testing.T, err error) {
	t.Helper()
	if _, ok := validation.Message(err); !ok {
		t.Errorf("erreur de saisie attendue, obtenu %v", err)
	}
}
