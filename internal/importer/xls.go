package importer

import (
	"encoding/binary"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/richardlehane/mscfb"

	"github.com/codecolors/buddyget/internal/platform/validation"
)

// Lecteur minimal des classeurs Excel 97-2003 (.xls, format BIFF8) : la
// première feuille, en texte. Il ne lit que ce qu'un relevé bancaire
// contient — textes, nombres, dates, résultats de formules — et rend une
// date au format JJ/MM/AAAA, un nombre avec un point décimal.
//
// Les exports Société Générale, par exemple, portent des montants au format
// « #,##0.00 » et des dates « dd/mm/yyyy » : le format d'affichage d'un
// nombre décide s'il s'agit d'une date.

// Types d'enregistrement BIFF8 utiles.
const (
	recBOF        = 0x0809
	recEOF        = 0x000A
	recFilePass   = 0x002F
	recDateMode   = 0x0022
	recFormat     = 0x041E
	recXF         = 0x00E0
	recSST        = 0x00FC
	recContinue   = 0x003C
	recBoundSheet = 0x0085
	recLabelSST   = 0x00FD
	recLabel      = 0x0204
	recNumber     = 0x0203
	recRK         = 0x027E
	recMulRK      = 0x00BD
	recBoolErr    = 0x0205
	recFormula    = 0x0006
	recString     = 0x0207
)

// xlsMaxCols borne les colonnes lues (un relevé en a une dizaine).
const xlsMaxCols = 256

var errXLSUnreadable = validation.Error("Classeur Excel (.xls) illisible : réexportez-le au format CSV ou XLSX.")

// record est un enregistrement BIFF : son type et ses données, suivies de
// celles de ses enregistrements CONTINUE.
type record struct {
	typ   uint16
	data  []byte
	parts []int // longueur de chaque segment (l'enregistrement puis ses CONTINUE)
}

// biffRecords découpe le flux Workbook en enregistrements, CONTINUE
// rattachés à l'enregistrement qui les précède.
func biffRecords(stream []byte) []record {
	var out []record
	for p := 0; p+4 <= len(stream); {
		typ := binary.LittleEndian.Uint16(stream[p:])
		n := int(binary.LittleEndian.Uint16(stream[p+2:]))
		if p+4+n > len(stream) {
			break
		}
		data := stream[p+4 : p+4+n]
		if typ == recContinue && len(out) > 0 {
			last := &out[len(out)-1]
			last.data = append(last.data, data...)
			last.parts = append(last.parts, n)
		} else {
			out = append(out, record{typ: typ, data: append([]byte(nil), data...), parts: []int{n}})
		}
		p += 4 + n
	}
	return out
}

// workbook réunit ce que la lecture d'une feuille demande.
type workbook struct {
	sst      []string
	xfFormat []uint16          // format de chaque style de cellule
	formats  map[uint16]string // formats personnalisés
	date1904 bool
}

// readXLS lit la première feuille de calcul d'un classeur .xls.
func readXLS(path string, fn rowFunc) error {
	stream, err := workbookStream(path)
	if err != nil {
		return err
	}
	return readWorkbook(stream, fn)
}

// readWorkbook lit la première feuille d'un flux Workbook BIFF8. Un flux
// malformé est refusé, jamais fatal.
func readWorkbook(stream []byte, fn rowFunc) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = errXLSUnreadable
		}
	}()
	recs := biffRecords(stream)
	wb := workbook{formats: map[uint16]string{}}
	sheetOffset := -1
	for _, r := range recs {
		switch r.typ {
		case recFilePass:
			return validation.Error("Classeur protégé par mot de passe : réexportez-le sans protection, au format CSV ou XLSX.")
		case recDateMode:
			wb.date1904 = len(r.data) >= 2 && binary.LittleEndian.Uint16(r.data) == 1
		case recFormat:
			if len(r.data) >= 2 {
				s := unicodeString(r.data[2:])
				wb.formats[binary.LittleEndian.Uint16(r.data)] = s
			}
		case recXF:
			if len(r.data) >= 4 {
				wb.xfFormat = append(wb.xfFormat, binary.LittleEndian.Uint16(r.data[2:]))
			}
		case recSST:
			wb.sst = sharedStrings(r)
		case recBoundSheet:
			// Première feuille de calcul (type 0) ; les graphiques sont ignorés.
			if sheetOffset < 0 && len(r.data) >= 6 && r.data[5] == 0 {
				sheetOffset = int(binary.LittleEndian.Uint32(r.data))
			}
		}
	}
	if sheetOffset < 0 || sheetOffset >= len(stream) {
		return validation.Error("Le classeur ne contient aucune feuille.")
	}
	return wb.readSheet(biffRecords(stream[sheetOffset:]), fn)
}

// workbookStream extrait le flux « Workbook » du conteneur OLE.
func workbookStream(path string) ([]byte, error) {
	f, err := os.Open(path) //nolint:gosec // G304 : chemin construit par le service, pas par l'utilisateur.
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	doc, err := mscfb.New(f)
	if err != nil {
		return nil, errXLSUnreadable
	}
	for entry, err := doc.Next(); err == nil; entry, err = doc.Next() {
		switch entry.Name {
		case "Workbook":
			return io.ReadAll(entry)
		case "Book":
			return nil, validation.Error("Classeur Excel 5/95, trop ancien : réexportez-le au format CSV ou XLSX.")
		}
	}
	return nil, errXLSUnreadable
}

// readSheet parcourt une feuille jusqu'à sa fin et transmet ses lignes dans
// l'ordre, lignes vides comprises.
func (wb *workbook) readSheet(recs []record, fn rowFunc) error {
	rows := map[int][]string{}
	maxRow := -1
	set := func(row, col int, v string) {
		if col >= xlsMaxCols {
			return
		}
		cells := rows[row]
		for len(cells) <= col {
			cells = append(cells, "")
		}
		cells[col] = strings.TrimSpace(v)
		rows[row] = cells
		maxRow = max(maxRow, row)
	}
	var pending [2]int // FORMULA dont le texte suit dans un enregistrement STRING
	pendingString := false
	for i, r := range recs {
		d := r.data
		if r.typ == recEOF || (r.typ == recBOF && i > 0) {
			break
		}
		if len(d) < 6 && r.typ != recString {
			continue
		}
		var row, col, xf int
		if len(d) >= 6 {
			row, col, xf = int(binary.LittleEndian.Uint16(d)), int(binary.LittleEndian.Uint16(d[2:])), int(binary.LittleEndian.Uint16(d[4:]))
		}
		switch r.typ {
		case recLabelSST:
			if len(d) >= 10 {
				if k := int(binary.LittleEndian.Uint32(d[6:])); k < len(wb.sst) {
					set(row, col, wb.sst[k])
				}
			}
		case recLabel:
			s := unicodeString(d[6:])
			set(row, col, s)
		case recNumber:
			if len(d) >= 14 {
				set(row, col, wb.number(math.Float64frombits(binary.LittleEndian.Uint64(d[6:])), xf))
			}
		case recRK:
			if len(d) >= 10 {
				set(row, col, wb.number(rkValue(binary.LittleEndian.Uint32(d[6:])), xf))
			}
		case recMulRK:
			// Ligne, première colonne, puis (style, valeur RK) par cellule.
			for c, p := col, 4; p+6 <= len(d)-2; c, p = c+1, p+6 {
				set(row, c, wb.number(rkValue(binary.LittleEndian.Uint32(d[p+2:])), int(binary.LittleEndian.Uint16(d[p:]))))
			}
		case recBoolErr:
			if len(d) >= 8 && d[7] == 0 {
				set(row, col, strconv.FormatBool(d[6] != 0))
			}
		case recFormula:
			if len(d) < 14 {
				continue
			}
			res := d[6:14]
			if res[6] == 0xFF && res[7] == 0xFF {
				switch res[0] {
				case 0: // texte : dans l'enregistrement STRING qui suit
					pending, pendingString = [2]int{row, col}, true
				case 1:
					set(row, col, strconv.FormatBool(res[2] != 0))
				}
				continue
			}
			set(row, col, wb.number(math.Float64frombits(binary.LittleEndian.Uint64(res)), xf))
		case recString:
			if pendingString {
				s := unicodeString(d)
				set(pending[0], pending[1], s)
				pendingString = false
			}
		}
	}
	for i := 0; i <= maxRow; i++ {
		if err := fn(i+1, rows[i]); err != nil {
			return err
		}
	}
	return nil
}

// number rend un nombre en texte : une date si son format en est une.
func (wb *workbook) number(v float64, xf int) string {
	if xf < len(wb.xfFormat) && isDateFormat(wb.xfFormat[xf], wb.formats[wb.xfFormat[xf]]) {
		epoch := time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC)
		if wb.date1904 {
			epoch = time.Date(1904, 1, 1, 0, 0, 0, 0, time.UTC)
		}
		return epoch.AddDate(0, 0, int(math.Floor(v))).Format("02/01/2006")
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// isDateFormat reconnaît un format de date : prédéfini (14 à 22, 45 à 47)
// ou personnalisé contenant jour, mois ou année hors texte littéral.
func isDateFormat(id uint16, custom string) bool {
	if (id >= 14 && id <= 22) || (id >= 45 && id <= 47) {
		return true
	}
	if custom == "" {
		return false
	}
	var b strings.Builder
	quoted := false
	for i := 0; i < len(custom); i++ {
		switch c := custom[i]; {
		case c == '"':
			quoted = !quoted
		case quoted:
		case c == '\\' || c == '_' || c == '*':
			i++ // caractère littéral, espacement ou remplissage
		case c == '[':
			for i < len(custom) && custom[i] != ']' {
				i++
			}
		default:
			b.WriteByte(c)
		}
	}
	s := strings.ToLower(b.String())
	return strings.ContainsAny(s, "dmy") && !strings.ContainsAny(s, "#0?")
}

// rkValue décode un nombre compressé RK.
func rkValue(rk uint32) float64 {
	var v float64
	if rk&2 != 0 {
		// Entier signé sur les 30 bits de poids fort.
		n := int64(rk >> 2)
		if rk&(1<<31) != 0 {
			n -= 1 << 30
		}
		v = float64(n)
	} else {
		v = math.Float64frombits(uint64(rk&^3) << 32)
	}
	if rk&1 != 0 {
		v /= 100
	}
	return v
}

// unicodeString lit une chaîne BIFF8 : longueur sur 16 bits, options,
// puis caractères.
func unicodeString(b []byte) string {
	if len(b) < 3 {
		return ""
	}
	n, flags, p := int(binary.LittleEndian.Uint16(b)), b[2], 3
	if flags&8 != 0 {
		p += 2 // nombre de séquences de mise en forme
	}
	if flags&4 != 0 {
		p += 4 // taille des données étendues
	}
	if p > len(b) {
		return ""
	}
	s, _ := chars(b[p:], n, flags&1 != 0)
	return s
}

// chars décode n caractères, sur un octet (Latin-1) ou deux (UTF-16).
func chars(b []byte, n int, wide bool) (string, int) {
	if !wide {
		n = min(n, len(b))
		r := make([]rune, n)
		for i := range n {
			r[i] = rune(b[i])
		}
		return string(r), n
	}
	n = min(n, len(b)/2)
	u := make([]uint16, n)
	for i := range n {
		u[i] = binary.LittleEndian.Uint16(b[2*i:])
	}
	return string(utf16.Decode(u)), 2 * n
}

// sharedStrings décode la table des chaînes partagées. Une chaîne peut
// franchir la limite d'un enregistrement CONTINUE : sa suite reprend alors
// par un octet d'options qui redonne la largeur des caractères.
func sharedStrings(r record) []string {
	d := r.data
	if len(d) < 8 {
		return nil
	}
	// Bornes des segments, pour repérer les franchissements.
	var bounds []int
	end := 0
	for _, n := range r.parts {
		end += n
		bounds = append(bounds, end)
	}
	segEnd := func(p int) int {
		for _, e := range bounds {
			if p < e {
				return e
			}
		}
		return len(d)
	}
	total := int(binary.LittleEndian.Uint32(d[4:]))
	out := make([]string, 0, min(total, len(d)/3))
	p := 8
	for range total {
		if p+3 > len(d) {
			break
		}
		n := int(binary.LittleEndian.Uint16(d[p:]))
		flags := d[p+2]
		p += 3
		rich, ext := 0, 0
		if flags&8 != 0 {
			rich, p = int(binary.LittleEndian.Uint16(d[p:])), p+2
		}
		if flags&4 != 0 {
			ext, p = int(binary.LittleEndian.Uint32(d[p:])), p+4
		}
		wide := flags&1 != 0
		var b strings.Builder
		for n > 0 && p < len(d) {
			e := segEnd(p)
			s, read := chars(d[p:e], n, wide)
			b.WriteString(s)
			p += read
			if wide {
				n -= read / 2
			} else {
				n -= read
			}
			if n > 0 && p == e && p < len(d) {
				wide = d[p]&1 != 0 // reprise dans le CONTINUE suivant
				p++
			}
		}
		p += 4*rich + ext
		out = append(out, b.String())
	}
	return out
}
