package importer

import (
	"encoding/binary"
	"math"
	"slices"
	"strings"
	"testing"

	"github.com/codecolors/buddyget/internal/platform/validation"
)

// biff assemble un flux Workbook BIFF8 enregistrement par enregistrement.
type biff []byte

func (b *biff) rec(typ uint16, parts ...[]byte) {
	data := slices.Concat(parts...)
	*b = binary.LittleEndian.AppendUint16(*b, typ)
	*b = binary.LittleEndian.AppendUint16(*b, uint16(len(data)))
	*b = append(*b, data...)
}

func u16(v ...uint16) []byte {
	var out []byte
	for _, x := range v {
		out = binary.LittleEndian.AppendUint16(out, x)
	}
	return out
}

func u32(v uint32) []byte { return binary.LittleEndian.AppendUint32(nil, v) }

// rkCents code un nombre de centièmes en RK (entier, divisé par 100).
func rkCents(v int32) uint32 { return uint32(v)<<2 | 3 }

func f64(v float64) []byte { return binary.LittleEndian.AppendUint64(nil, math.Float64bits(v)) }

// str16 code une chaîne BIFF8 à longueur sur 16 bits, en UTF-16.
func str16(s string) []byte {
	r := []rune(s)
	out := append(u16(uint16(len(r))), 1)
	for _, c := range r {
		out = binary.LittleEndian.AppendUint16(out, uint16(c))
	}
	return out
}

// sgWorkbook reproduit la structure d'un export Société Générale : ligne de
// synthèse, ligne vide, en-tête en ligne 3, dates « dd/mm/yyyy », montants
// au format prédéfini 4 (« #,##0.00 ») et table de chaînes coupée par un
// CONTINUE au milieu d'une chaîne.
func sgWorkbook() []byte {
	const (
		xfGeneral = 0
		xfDate    = 1
		xfAmount  = 2
		xfCustom  = 3
	)
	strs := []string{"Date de l'opération", "Libellé", "Détail de l'écriture", "Montant de l'opération", "Devise", "EUR", "CARTE X4321 BOULANGERIE", "PRLV SEPA EDF"}
	sst := slices.Concat(u32(uint32(len(strs))), u32(uint32(len(strs))))
	for _, s := range strs {
		sst = append(sst, str16(s)...)
	}
	// Coupure au milieu de « Détail de l'écriture » : la suite reprend par
	// un octet d'options (1 : UTF-16).
	cut := 8 + len(str16(strs[0])) + len(str16(strs[1])) + 3 + 2*6

	var g biff
	g.rec(recBOF, u16(0x0600, 0x0005), make([]byte, 12))
	g.rec(recDateMode, u16(0))
	g.rec(recFormat, u16(164), str16("dd/mm/yyyy"))
	g.rec(recFormat, u16(165), str16(`#,##0.00" €"`))
	for _, f := range []uint16{0, 164, 4, 165} {
		g.rec(recXF, u16(0, f), make([]byte, 16))
	}
	g.rec(recSST, sst[:cut])
	g.rec(recContinue, []byte{1}, sst[cut:])
	bound := len(g)
	g.rec(recBoundSheet, u32(0), u16(0), str16("Feuille"))
	g.rec(recEOF)

	var s biff
	s.rec(recBOF, u16(0x0600, 0x0010), make([]byte, 12))
	// Ligne 1 : synthèse (texte en ligne, nombre RK entier).
	s.rec(recLabel, u16(0, 0, xfGeneral), str16("00012345678"))
	s.rec(recRK, u16(0, 1, xfGeneral), u32(42<<2|2))
	s.rec(recFormula, u16(0, 2, xfGeneral), f64(1250.5), make([]byte, 8))
	// Ligne 3 : en-tête.
	for c := range 5 {
		s.rec(recLabelSST, u16(2, uint16(c), xfGeneral), u32(uint32(c)))
	}
	// Ligne 4 : 30/10/2026 (46325), RK au centième.
	s.rec(recNumber, u16(3, 0, xfDate), f64(46325))
	s.rec(recLabelSST, u16(3, 1, xfGeneral), u32(6))
	s.rec(recFormula, u16(3, 2, xfGeneral), []byte{0, 0, 0, 0, 0, 0, 0xFF, 0xFF}, make([]byte, 8))
	s.rec(recString, str16("CARTE X4321 29/10 BOULANGERIE MARTIN"))
	s.rec(recRK, u16(3, 3, xfAmount), u32(rkCents(-420)))
	s.rec(recLabelSST, u16(3, 4, xfGeneral), u32(5))
	// Ligne 5 : date et montant dans un MULRK, format personnalisé.
	s.rec(recMulRK, u16(4, 0), u16(xfDate), u32(46324<<2|2), u16(xfGeneral), u32(0), u16(1))
	s.rec(recLabelSST, u16(4, 1, xfGeneral), u32(7))
	s.rec(recRK, u16(4, 3, xfCustom), u32(rkCents(-7832)))
	s.rec(recLabelSST, u16(4, 4, xfGeneral), u32(5))
	s.rec(recEOF)

	binary.LittleEndian.PutUint32(g[bound+4:], uint32(len(g)))
	return slices.Concat(g, s)
}

func TestReadWorkbook(t *testing.T) {
	var rows []string
	err := readWorkbook(sgWorkbook(), func(n int, cells []string) error {
		rows = append(rows, strings.Join(cells, "|"))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"00012345678|42|1250.5",
		"",
		"Date de l'opération|Libellé|Détail de l'écriture|Montant de l'opération|Devise",
		"30/10/2026|CARTE X4321 BOULANGERIE|CARTE X4321 29/10 BOULANGERIE MARTIN|-4.2|EUR",
		"29/10/2026|PRLV SEPA EDF||-78.32|EUR",
	}
	if !slices.Equal(rows, want) {
		t.Errorf("lignes :\n obtenu  %q\n attendu %q", rows, want)
	}
}

func TestReadWorkbookSGProfile(t *testing.T) {
	var head []HeadRow
	var cells [][]string
	if err := readWorkbook(sgWorkbook(), func(n int, c []string) error {
		head = append(head, HeadRow{n, slices.Clone(c)})
		cells = append(cells, slices.Clone(c))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	p, m, ok := detect(Profiles(t.TempDir()), head)
	if !ok || p.Name != "Société Générale" || m.HeaderLine != 3 {
		t.Fatalf("profil %q (ligne %d)", p.Name, m.HeaderLine)
	}
	l, ok := m.parse(4, cells[3], true)
	if !ok || l != (Line{4, "2026-10-30", "CARTE X4321 29/10 BOULANGERIE MARTIN", -420, ""}) {
		t.Errorf("ligne 4 : %v", l)
	}
}

func TestReadWorkbookMalformed(t *testing.T) {
	for name, stream := range map[string][]byte{
		"vide":            nil,
		"tronqué":         sgWorkbook()[:200],
		"protégé":         slices.Concat(sgWorkbook()[:20], []byte{0x2F, 0, 0, 0}),
		"feuille absente": {0x09, 0x08, 0, 0},
	} {
		t.Run(name, func(t *testing.T) {
			err := readWorkbook(stream, func(int, []string) error { return nil })
			if _, ok := validation.Message(err); !ok {
				t.Errorf("erreur %v, attendu un refus explicite", err)
			}
		})
	}
}

func TestIsDateFormat(t *testing.T) {
	for _, c := range []struct {
		id     uint16
		custom string
		want   bool
	}{
		{14, "", true},
		{4, "", false},
		{0, "", false},
		{164, "dd/mm/yyyy", true},
		{165, `#,##0.00" €"`, false},
		{166, `[$-40C]d mmmm yyyy`, true},
		{167, `0.00" jours"`, false},
		{168, `"md"0`, false},
	} {
		if got := isDateFormat(c.id, c.custom); got != c.want {
			t.Errorf("isDateFormat(%d, %q) = %v", c.id, c.custom, got)
		}
	}
}

func TestRKValue(t *testing.T) {
	for rk, want := range map[uint32]float64{
		42<<2 | 2:                           42,
		rkCents(-420):                       -4.2,
		uint32(math.Float64bits(1.5) >> 32): 1.5,
	} {
		if got := rkValue(rk); got != want {
			t.Errorf("rkValue(%#x) = %v, attendu %v", rk, got, want)
		}
	}
}

// textWorkbook fabrique un classeur d'une feuille dont chaque cellule est un
// texte en ligne (enregistrements LABEL).
func textWorkbook(rows [][]string) []byte {
	var g biff
	g.rec(recBOF, u16(0x0600, 0x0005), make([]byte, 12))
	g.rec(recXF, u16(0, 0), make([]byte, 16))
	bound := len(g)
	g.rec(recBoundSheet, u32(0), u16(0), str16("Feuille"))
	g.rec(recEOF)
	var s biff
	s.rec(recBOF, u16(0x0600, 0x0010), make([]byte, 12))
	for r, cells := range rows {
		for c, v := range cells {
			if v != "" {
				s.rec(recLabel, u16(uint16(r), uint16(c), 0), str16(v))
			}
		}
	}
	s.rec(recEOF)
	binary.LittleEndian.PutUint32(g[bound+4:], uint32(len(g)))
	return slices.Concat(g, s)
}

// L'export BNP Paribas : synthèse, ligne vide, en-tête sans accents en
// ligne 3, dates en texte JJ-MM-AAAA.
func TestReadWorkbookBNPProfile(t *testing.T) {
	stream := textWorkbook([][]string{
		{"Compte de chèques ****1234", "Solde au 06/11/2026", "250.00", "EUR"},
		{},
		{"Date operation", "Categorie operation", "Sous Categorie operation", "Libelle operation", "Montant operation", "Pointage operation", "Commentaire operation"},
		{"05-11-2026", "Vie Quotidienne", "Alimentation, supermarché", "PAIEMENT CB SUPERMARCHE DU 05/11/26", "-6.21"},
	})
	var head []HeadRow
	if err := readWorkbook(stream, func(n int, c []string) error {
		head = append(head, HeadRow{n, slices.Clone(c)})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	p, m, ok := detect(Profiles(t.TempDir()), head)
	if !ok || p.Name != "BNP Paribas" || m.HeaderLine != 3 {
		t.Fatalf("profil %q (ligne %d)", p.Name, m.HeaderLine)
	}
	l, ok := m.parse(4, head[3].Cells, true)
	if !ok || l != (Line{4, "2026-11-05", "PAIEMENT CB SUPERMARCHE DU 05/11/26", -621, ""}) {
		t.Errorf("ligne 4 : %v", l)
	}
}
