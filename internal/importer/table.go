package importer

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"errors"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/extrame/xls"
	"github.com/xuri/excelize/v2"
	"golang.org/x/text/encoding/charmap"

	"github.com/codecolors/buddyget/internal/platform/validation"
)

// Format est le format réel d'un fichier, reconnu à sa signature et non à
// son extension (certaines banques nomment « .xls » un simple CSV).
type Format string

const (
	FormatCSV  Format = "csv"
	FormatXLSX Format = "xlsx"
	FormatXLS  Format = "xls"
)

// errStop interrompt volontairement un parcours de lignes.
var errStop = errors.New("arrêt de la lecture")

// rowFunc reçoit le numéro de ligne dans le fichier (à partir de 1) et ses
// cellules. La tranche n'est valable que pendant l'appel.
type rowFunc func(line int, cells []string) error

// detectFormat reconnaît le format d'après les premiers octets.
func detectFormat(head []byte) (Format, error) {
	trimmed := bytes.TrimLeft(bytes.TrimPrefix(head, utf8BOM), " \t\r\n")
	switch {
	case bytes.HasPrefix(head, []byte{0xd0, 0xcf, 0x11, 0xe0}):
		return FormatXLS, nil
	case bytes.HasPrefix(head, []byte("PK\x03\x04")):
		return FormatXLSX, nil
	case bytes.HasPrefix(trimmed, []byte("<")):
		return "", validation.Error("Ce fichier est une page web (HTML) et non un tableau : réexportez vos opérations au format CSV ou Excel.")
	}
	return FormatCSV, nil
}

// sniffFile reconnaît le format d'un fichier enregistré.
func sniffFile(path string) (Format, error) {
	f, err := os.Open(path) //nolint:gosec // G304 : chemin construit par le service, pas par l'utilisateur.
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	head := make([]byte, 512)
	n, err := io.ReadFull(f, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return "", err
	}
	return detectFormat(head[:n])
}

// readRows parcourt les lignes de la première feuille (ou du CSV). Le CSV et
// le XLSX sont lus en flux ; un XLS, limité à 65 536 lignes, est chargé.
func readRows(path string, format Format, fn rowFunc) error {
	var err error
	switch format {
	case FormatXLSX:
		err = readXLSX(path, fn)
	case FormatXLS:
		err = readXLS(path, fn)
	default:
		err = readCSV(path, fn)
	}
	if errors.Is(err, errStop) {
		return nil
	}
	return err
}

var utf8BOM = []byte{0xef, 0xbb, 0xbf}

// readCSV décode le fichier (UTF-8, avec ou sans BOM, sinon Windows-1252,
// sur-ensemble d'ISO-8859-1) et devine le séparateur.
func readCSV(path string, fn rowFunc) error {
	f, err := os.Open(path) //nolint:gosec // G304 : chemin construit par le service, pas par l'utilisateur.
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	br := bufio.NewReaderSize(f, 64<<10)
	head, err := br.Peek(64 << 10)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, bufio.ErrBufferFull) {
		return err
	}
	var r io.Reader = br
	switch {
	case bytes.HasPrefix(head, utf8BOM):
		if _, err := br.Discard(len(utf8BOM)); err != nil {
			return err
		}
		head = head[len(utf8BOM):]
	case !validUTF8Prefix(head):
		r = charmap.Windows1252.NewDecoder().Reader(br)
		if head, err = charmap.Windows1252.NewDecoder().Bytes(head); err != nil {
			return err
		}
	}

	cr := csv.NewReader(r)
	cr.Comma = sniffSeparator(head)
	cr.FieldsPerRecord = -1
	cr.LazyQuotes = true
	cr.ReuseRecord = true
	for {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			return nil
		}
		var pe *csv.ParseError
		if errors.As(err, &pe) {
			return validation.Errorf("Fichier CSV illisible à la ligne %d.", pe.Line)
		}
		if err != nil {
			return err
		}
		line, _ := cr.FieldPos(0)
		if err := fn(line, rec); err != nil {
			return err
		}
	}
}

// validUTF8Prefix tolère un caractère coupé en fin d'échantillon.
func validUTF8Prefix(b []byte) bool {
	for i := 0; i < utf8.UTFMax && len(b) > 0; i++ {
		if utf8.Valid(b) {
			return true
		}
		b = b[:len(b)-1]
	}
	return utf8.Valid(b)
}

// sniffSeparator choisit, parmi ; , tabulation et |, celui qui donne le plus
// de lignes ayant le même nombre (≥ 2) de colonnes dans l'échantillon.
func sniffSeparator(sample []byte) rune {
	best, bestScore := ';', 0
	for _, sep := range []rune{';', ',', '\t', '|'} {
		cr := csv.NewReader(bytes.NewReader(sample))
		cr.Comma, cr.FieldsPerRecord, cr.LazyQuotes = sep, -1, true
		counts := map[int]int{}
		for range 30 {
			rec, err := cr.Read()
			if err != nil {
				break
			}
			if len(rec) > 1 {
				counts[len(rec)]++
			}
		}
		for _, n := range counts {
			if n > bestScore {
				best, bestScore = sep, n
			}
		}
	}
	return best
}

// readXLSX lit la première feuille en flux, valeurs brutes : les dates
// arrivent en numéro de série Excel et les montants sans format.
func readXLSX(path string, fn rowFunc) error {
	f, err := excelize.OpenFile(path)
	if err != nil {
		return validation.Error("Classeur Excel (.xlsx) illisible.")
	}
	defer func() { _ = f.Close() }()
	sheets := f.GetSheetList()
	if len(sheets) == 0 {
		return validation.Error("Le classeur ne contient aucune feuille.")
	}
	rows, err := f.Rows(sheets[0])
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for line := 1; rows.Next(); line++ {
		cells, err := rows.Columns(excelize.Options{RawCellValue: true})
		if err != nil {
			return err
		}
		if err := fn(line, cells); err != nil {
			return err
		}
	}
	return rows.Error()
}

// readXLS lit la première feuille d'un classeur Excel 97-2003. La
// bibliothèque peut paniquer sur un fichier corrompu : la panique devient
// une erreur de saisie.
func readXLS(path string, fn rowFunc) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = validation.Errorf("Classeur Excel (.xls) illisible (%v) : réexportez-le au format CSV ou XLSX.", r)
		}
	}()
	wb, closer, err := xls.OpenWithCloser(path, "utf-8")
	if err != nil {
		return validation.Error("Classeur Excel (.xls) illisible : réexportez-le au format CSV ou XLSX.")
	}
	defer func() { _ = closer.Close() }()
	sheet := wb.GetSheet(0)
	if sheet == nil {
		return validation.Error("Le classeur ne contient aucune feuille.")
	}
	var cells []string
	for i := 0; i <= int(sheet.MaxRow); i++ {
		cells = cells[:0]
		if row := sheet.Row(i); row != nil {
			for c := 0; c < row.LastCol(); c++ { // LastCol : dernière colonne + 1
				cells = append(cells, strings.TrimSpace(row.Col(c)))
			}
		}
		if err := fn(i+1, cells); err != nil {
			return err
		}
	}
	return nil
}

// Describe résume un format pour l'affichage.
func (f Format) Describe() string {
	switch f {
	case FormatXLSX:
		return "Excel (.xlsx)"
	case FormatXLS:
		return "Excel 97-2003 (.xls)"
	}
	return "CSV"
}
