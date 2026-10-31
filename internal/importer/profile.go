package importer

import (
	"embed"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode"

	"go.yaml.in/yaml/v3"

	"github.com/codecolors/buddyget/internal/platform/text"
	"github.com/codecolors/buddyget/internal/platform/validation"
)

//go:embed profiles/*.yaml
var builtinProfiles embed.FS

// builtinOrder fixe l'ordre d'essai des profils fournis : le générique,
// le moins exigeant, en dernier.
var builtinOrder = []string{"sg.yaml", "bnp.yaml", "generique.yaml"}

// detectLines est le nombre de premières lignes où chercher l'en-tête.
const detectLines = 30

// Profile décrit un format d'export : pour chaque donnée, les intitulés de
// colonne possibles. Il est reconnu quand la ligne d'en-tête du fichier
// contient une colonne de date, de libellé et de montant (ou de débit et de
// crédit).
type Profile struct {
	Name    string `yaml:"nom"`
	Columns struct {
		Date   []string `yaml:"date"`
		Label  []string `yaml:"libelle"`
		Amount []string `yaml:"montant,omitempty"`
		Debit  []string `yaml:"debit,omitempty"`
		Credit []string `yaml:"credit,omitempty"`
	} `yaml:"colonnes"`
	DateFormat string `yaml:"format_date,omitempty"`
	Invert     bool   `yaml:"montants_inverses,omitempty"`
}

// resolve cherche les colonnes du profil dans une ligne d'en-tête.
func (p Profile) resolve(header []string) (Mapping, bool) {
	folded := make([]string, len(header))
	for i, h := range header {
		folded[i] = text.Fold(h)
	}
	find := func(names []string) []int {
		var idx []int
		for _, n := range names {
			if i := slices.Index(folded, text.Fold(n)); i >= 0 && !slices.Contains(idx, i) {
				idx = append(idx, i)
			}
		}
		return idx
	}
	first := func(names []string) int {
		if idx := find(names); len(idx) > 0 {
			return idx[0]
		}
		return NoColumn
	}
	m := Mapping{
		Date:       first(p.Columns.Date),
		Labels:     find(p.Columns.Label),
		Amount:     first(p.Columns.Amount),
		Debit:      first(p.Columns.Debit),
		Credit:     first(p.Columns.Credit),
		DateFormat: p.DateFormat,
		Invert:     p.Invert,
	}
	if m.Amount >= 0 {
		m.Debit, m.Credit = NoColumn, NoColumn
	} else if m.Debit < 0 || m.Credit < 0 {
		return m, false // débit et crédit vont ensemble
	}
	return m, m.validate() == nil
}

// Profiles lit les profils : ceux de dir d'abord (personnels ou fournis
// remplacés, à nom de fichier égal), puis les profils fournis. Un fichier
// illisible est signalé dans le journal et ignoré.
func Profiles(dir string) []Profile {
	custom := map[string]Profile{}
	var names []string
	entries, err := os.ReadDir(dir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		slog.Warn("lecture des profils d'import", "dossier", dir, "err", err)
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".yaml" {
			continue
		}
		p, err := loadProfile(os.DirFS(dir), e.Name())
		if err != nil {
			slog.Warn("profil d'import ignoré", "fichier", e.Name(), "err", err)
			continue
		}
		custom[e.Name()] = p
		if !slices.Contains(builtinOrder, e.Name()) {
			names = append(names, e.Name())
		}
	}

	var out []Profile
	for _, n := range names {
		out = append(out, custom[n])
	}
	for _, n := range builtinOrder {
		if p, ok := custom[n]; ok {
			out = append(out, p)
			continue
		}
		p, err := loadProfile(builtinProfiles, "profiles/"+n)
		if err != nil {
			panic(err) // fichier embarqué : vérifié par les tests
		}
		out = append(out, p)
	}
	return out
}

func loadProfile(fsys fs.FS, name string) (Profile, error) {
	data, err := fs.ReadFile(fsys, name)
	if err != nil {
		return Profile{}, err
	}
	var p Profile
	if err := yaml.Unmarshal(data, &p); err != nil {
		return Profile{}, err
	}
	if p.Name == "" || len(p.Columns.Date) == 0 || len(p.Columns.Label) == 0 {
		return Profile{}, errors.New("nom, colonnes.date et colonnes.libelle sont obligatoires")
	}
	if p.DateFormat != "" && layout(p.DateFormat) == "" {
		return Profile{}, errors.New("format_date inconnu")
	}
	return p, nil
}

// HeadRow est une des premières lignes d'un fichier, avec son numéro (les
// lignes vides d'un CSV ne sont pas transmises par le lecteur).
type HeadRow struct {
	Line  int
	Cells []string
}

// detect cherche, dans les premières lignes, un en-tête reconnu par l'un des
// profils.
func detect(profiles []Profile, head []HeadRow) (Profile, Mapping, bool) {
	for _, row := range head {
		for _, p := range profiles {
			if m, ok := p.resolve(row.Cells); ok {
				m.HeaderLine = row.Line
				return p, m, true
			}
		}
	}
	return Profile{}, Mapping{}, false
}

// SaveProfile enregistre dans dir un profil tiré d'une correspondance
// manuelle, d'après les intitulés de sa ligne d'en-tête. Il sera reconnu
// automatiquement aux imports suivants.
func SaveProfile(dir, name string, header []string, m Mapping) (Profile, error) {
	p := Profile{Name: strings.TrimSpace(name), DateFormat: m.DateFormat, Invert: m.Invert}
	file := slug(p.Name)
	if file == "" {
		return p, validation.Error("Donnez un nom au profil.")
	}
	if m.HeaderLine == 0 {
		return p, validation.Error("Un profil se reconnaît à sa ligne d'en-tête : indiquez-la pour l'enregistrer.")
	}
	col := func(i int) []string {
		if i < 0 || i >= len(header) || strings.TrimSpace(header[i]) == "" {
			return nil
		}
		return []string{strings.TrimSpace(header[i])}
	}
	p.Columns.Date, p.Columns.Amount = col(m.Date), col(m.Amount)
	p.Columns.Debit, p.Columns.Credit = col(m.Debit), col(m.Credit)
	for _, i := range m.Labels {
		p.Columns.Label = append(p.Columns.Label, col(i)...)
	}
	if _, ok := p.resolve(header); !ok {
		return p, validation.Error("Les colonnes choisies n'ont pas toutes un intitulé dans la ligne d'en-tête.")
	}
	data, err := yaml.Marshal(p)
	if err != nil {
		return p, err
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return p, err
	}
	data = append([]byte("# Profil créé depuis l'assistant d'import ; modifiable à la main.\n"), data...)
	return p, os.WriteFile(filepath.Join(dir, file+".yaml"), data, 0o600)
}

// slug dérive un nom de fichier sûr : « Crédit Agricole » → « credit-agricole ».
func slug(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range text.Fold(name) {
		if unicode.IsLetter(r) && r < unicode.MaxASCII || unicode.IsDigit(r) {
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(r)
			dash = false
		} else {
			dash = true
		}
	}
	return b.String()
}
