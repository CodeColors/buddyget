package importer

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/codecolors/buddyget/internal/platform/validation"
)

// rowsOf lit un fichier entier : format détecté et lignes non vides.
func rowsOf(t *testing.T, path string) (Format, []string) {
	t.Helper()
	format, err := sniffFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	err = readRows(path, format, func(line int, cells []string) error {
		if !blank(cells) {
			out = append(out, fmt.Sprintf("%d:%s", line, strings.Join(cells, "|")))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return format, out
}

func writeFile(t *testing.T, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestReadFixtures(t *testing.T) {
	for _, c := range []struct {
		file   string
		format Format
		want   []string // lignes attendues (début)
	}{
		{"../../testdata/sg/releve-octobre.csv", FormatCSV, []string{
			`1:="00012345678"|Compte Bancaire |`,
			"3:Date de l'opération|Libellé|Détail de l'écriture|Montant de l'opération|Devise",
			"4:31/10/2026|CARTE X4321 30/10 BOULANGERIE|CARTE X4321 30/10 BOULANGERIE MARTIN 75 PARIS|-4,20|EUR",
		}},
		{"../../testdata/bnp/releve-octobre.xlsx", FormatXLSX, []string{
			"1:Compte de chèques ****4321",
			"2:Date opération|Catégorie opération|Sous-catégorie|Libellé opération|Montant opération",
			"3:46326|Alimentation|Supermarché|CB CARREFOUR MARKET 30/10|-82.15",
		}},
		{"../../testdata/generique/debit-credit.xls", FormatXLS, []string{
			"1:Date|Libellé|Débit|Crédit",
			"2:15/10/2026|Retrait DAB|40",
		}},
		{"../../testdata/generique/sans-entete.csv", FormatCSV, []string{
			"1:2026-10-01|Loyer octobre|-750.00",
		}},
	} {
		t.Run(filepath.Base(c.file), func(t *testing.T) {
			format, rows := rowsOf(t, c.file)
			if format != c.format {
				t.Errorf("format %s, attendu %s", format, c.format)
			}
			for i, w := range c.want {
				if i >= len(rows) || rows[i] != w {
					t.Errorf("ligne %d :\n obtenu  %q\n attendu %q", i, at(rows, i), w)
				}
			}
		})
	}
}

func at(s []string, i int) string {
	if i < len(s) {
		return s[i]
	}
	return "(absente)"
}

func TestReadCSVVariants(t *testing.T) {
	for name, c := range map[string]struct {
		data []byte
		want string
	}{
		"UTF-8 avec BOM, virgule et guillemets": {
			append([]byte{0xef, 0xbb, 0xbf}, "Date,Libellé,Montant\n01/10/2026,\"Café, croissant\",\"-3,50\"\n"...),
			"2:01/10/2026|Café, croissant|-3,50",
		},
		"Windows-1252 et point-virgule": {
			[]byte("Date;Libell\xe9;Montant\r\n01/10/2026;Caf\xe9 \x80;-3,50\r\n"),
			"2:01/10/2026|Café €|-3,50",
		},
		"tabulation": {
			[]byte("Date\tLibellé\tMontant\n01/10/2026\tLoyer\t-700\n"),
			"2:01/10/2026|Loyer|-700",
		},
		"champ sur plusieurs lignes": {
			[]byte("Date;Libellé;Montant\n01/10/2026;\"Virement\nloyer\";-700\n02/10/2026;Pain;-1\n"),
			"4:02/10/2026|Pain|-1",
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, rows := rowsOf(t, writeFile(t, "f.csv", c.data))
			if rows[len(rows)-1] != c.want {
				t.Errorf("dernière ligne %q, attendu %q", rows[len(rows)-1], c.want)
			}
		})
	}
}

func TestUnreadableFiles(t *testing.T) {
	for name, data := range map[string][]byte{
		"page HTML déguisée en .xls": []byte("\n<html><table><tr><td>Date</td></tr></table></html>"),
		"faux XLS":                   append([]byte{0xd0, 0xcf, 0x11, 0xe0}, make([]byte, 600)...),
		"faux XLSX":                  []byte("PK\x03\x04pas une archive"),
	} {
		t.Run(name, func(t *testing.T) {
			p := writeFile(t, "f.xls", data)
			format, err := sniffFile(p)
			if err == nil {
				err = readRows(p, format, func(int, []string) error { return nil })
			}
			if _, ok := validation.Message(err); !ok {
				t.Errorf("erreur de saisie attendue, obtenu %v", err)
			}
		})
	}
}

func TestSniffSeparator(t *testing.T) {
	for sample, want := range map[string]rune{
		"a;b;c\n1;2;3\n": ';',
		"a,b,c\n1,2,3\n": ',',
		"Date;Libellé;Montant\n1;\"x, y\";-3,50\n": ';',
		"a\tb\n1\t2\n":        '\t',
		"une seule colonne\n": ';',
	} {
		if got := sniffSeparator([]byte(sample)); got != want {
			t.Errorf("sniffSeparator(%q) = %q, attendu %q", sample, got, want)
		}
	}
}
