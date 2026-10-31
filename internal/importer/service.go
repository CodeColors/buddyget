// Package importer importe les relevés bancaires (CSV, XLSX, XLS) en trois
// étapes : dépôt du fichier et reconnaissance de son format, aperçu des
// lignes (nouvelles, doublons, erreurs) avec sélection, puis validation.
// Un import validé reste annulable.
package importer

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/codecolors/buddyget/internal/platform/calendar"
	"github.com/codecolors/buddyget/internal/platform/db"
	"github.com/codecolors/buddyget/internal/platform/validation"
)

const (
	// PageSize est le nombre de lignes affichées par page d'aperçu.
	PageSize = 100
	// draftTTL est la durée de conservation d'un import non validé.
	draftTTL = 24 * time.Hour
	// stageBatch est le nombre de lignes insérées par requête.
	stageBatch = 200
	// labelMaxLen borne les libellés importés, comme ceux saisis.
	labelMaxLen = 200
)

// ErrNotFound signale un import ou un brouillon inexistant.
var ErrNotFound = errors.New("import introuvable")

// Statuts d'une ligne en attente.
const (
	StatusNew       = "nouvelle"
	StatusDuplicate = "doublon"
	StatusError     = "erreur"
)

// Service conduit les imports.
type Service struct {
	db          *db.DB
	uploads     string // fichiers déposés en attente de validation
	profilesDir string
	maxBytes    int64
	now         func() time.Time
}

// NewService crée le service d'import ; les fichiers en attente vont dans
// dataDir/imports, les profils personnels dans dataDir/profiles.
func NewService(d *db.DB, dataDir string, maxUploadBytes int64) *Service {
	return &Service{
		db:          d,
		uploads:     filepath.Join(dataDir, "imports"),
		profilesDir: filepath.Join(dataDir, "profiles"),
		maxBytes:    maxUploadBytes,
		now:         time.Now,
	}
}

// Draft est un import en cours.
type Draft struct {
	Token       string
	AccountID   int64
	AccountName string
	Filename    string
	Format      Format
	Profile     string   // profil reconnu, ou nom du profil enregistré ; vide sinon
	Mapping     *Mapping // nil tant que les colonnes ne sont pas connues
	Counts      Counts
}

// Counts résume les lignes d'un import en cours.
type Counts struct {
	New, Duplicate, Error, Selected int
}

// Total renvoie le nombre de lignes de données lues.
func (c Counts) Total() int { return c.New + c.Duplicate + c.Error }

// StagedLine est une ligne en attente.
type StagedLine struct {
	Line     int
	Date     calendar.Date
	Label    string
	Amount   int64
	Status   string
	Error    string
	Selected bool
}

// Import est un import validé.
type Import struct {
	ID          int64
	AccountID   int64
	AccountName string
	Filename    string
	Profile     string
	ImportedAt  time.Time
	Read        int
	Added       int
	Duplicate   int
	Errors      int
	Skipped     int
	Undone      bool
}

// Upload enregistre un fichier pour le compte choisi, reconnaît son format
// et son profil, et met ses lignes en attente si le profil est reconnu.
// Renvoie le jeton du brouillon.
func (s *Service) Upload(ctx context.Context, accountID int64, filename string, src io.Reader) (string, error) {
	if err := s.purge(ctx); err != nil {
		return "", err
	}
	ok, err := s.exists(ctx, `SELECT EXISTS (SELECT 1 FROM accounts WHERE id = ? AND archived = 0)`, accountID)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", validation.Error("Choisissez le compte qui reçoit les opérations.")
	}
	filename = strings.TrimSpace(filepath.Base(filename))
	if filename == "" || filename == "." {
		return "", validation.Error("Choisissez un fichier.")
	}

	token := rand.Text()
	path, err := s.save(token, src)
	if err != nil {
		return "", err
	}
	draft, head, err := s.inspect(path)
	if err == nil {
		_, err = s.db.W.ExecContext(ctx,
			`INSERT INTO import_drafts (token, account_id, filename, format, created_at) VALUES (?, ?, ?, ?, ?)`,
			token, accountID, filename, draft.Format, s.now().Unix())
	}
	if err == nil {
		if p, m, found := detect(Profiles(s.profilesDir), head); found {
			err = s.stage(ctx, token, accountID, draft.Format, p.Name, m)
			if _, invalid := validation.Message(err); invalid {
				err = nil // profil inapplicable : l'utilisateur choisira les colonnes
			}
		}
	}
	if err != nil {
		return "", errors.Join(err, s.Cancel(ctx, token))
	}
	return token, nil
}

// save copie le fichier déposé en refusant ce qui dépasse la taille permise.
func (s *Service) save(token string, src io.Reader) (string, error) {
	if err := os.MkdirAll(s.uploads, 0o750); err != nil {
		return "", err
	}
	path := filepath.Join(s.uploads, token)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) //nolint:gosec // G304 : jeton aléatoire.
	if err != nil {
		return "", err
	}
	n, err := io.Copy(f, io.LimitReader(src, s.maxBytes+1))
	err = errors.Join(err, f.Close())
	switch {
	case err != nil:
	case n == 0:
		err = validation.Error("Le fichier est vide.")
	case n > s.maxBytes:
		err = validation.Errorf("Le fichier dépasse la taille maximale de %d Mo.", s.maxBytes>>20)
	}
	if err != nil {
		return "", errors.Join(err, os.Remove(path))
	}
	return path, nil
}

// inspect reconnaît le format du fichier et lit ses premières lignes.
func (s *Service) inspect(path string) (Draft, []HeadRow, error) {
	format, err := sniffFile(path)
	if err != nil {
		return Draft{}, nil, err
	}
	var head []HeadRow
	err = readRows(path, format, func(line int, cells []string) error {
		if !blank(cells) {
			head = append(head, HeadRow{line, slices.Clone(cells)})
		}
		if len(head) == detectLines {
			return errStop
		}
		return nil
	})
	if err == nil && len(head) == 0 {
		err = validation.Error("Le fichier ne contient aucune ligne.")
	}
	return Draft{Format: format}, head, err
}

// Draft renvoie un import en cours avec le décompte de ses lignes.
func (s *Service) Draft(ctx context.Context, token string) (Draft, error) {
	d := Draft{Token: token}
	var mapping string
	err := s.db.R.QueryRowContext(ctx, `
		SELECT d.account_id, a.name, d.filename, d.format, d.profile, d.mapping
		FROM import_drafts d JOIN accounts a ON a.id = d.account_id
		WHERE d.token = ?`, token,
	).Scan(&d.AccountID, &d.AccountName, &d.Filename, &d.Format, &d.Profile, &mapping)
	if errors.Is(err, sql.ErrNoRows) {
		return d, ErrNotFound
	}
	if err != nil {
		return d, err
	}
	if mapping != "" {
		d.Mapping = new(Mapping)
		if err := json.Unmarshal([]byte(mapping), d.Mapping); err != nil {
			return d, err
		}
	}
	err = s.db.R.QueryRowContext(ctx, `
		SELECT count(*) FILTER (WHERE status = 'nouvelle'),
		       count(*) FILTER (WHERE status = 'doublon'),
		       count(*) FILTER (WHERE status = 'erreur'),
		       count(*) FILTER (WHERE selected = 1)
		FROM import_lines WHERE token = ?`, token,
	).Scan(&d.Counts.New, &d.Counts.Duplicate, &d.Counts.Error, &d.Counts.Selected)
	return d, err
}

// Pending liste les imports en cours, du plus récent au plus ancien.
func (s *Service) Pending(ctx context.Context) ([]Draft, error) {
	rows, err := s.db.R.QueryContext(ctx, `
		SELECT d.token, a.name, d.filename FROM import_drafts d JOIN accounts a ON a.id = d.account_id
		WHERE d.created_at > ? ORDER BY d.created_at DESC`, s.now().Add(-draftTTL).Unix())
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Draft
	for rows.Next() {
		var d Draft
		if err := rows.Scan(&d.Token, &d.AccountName, &d.Filename); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// Head renvoie les premières lignes non vides du fichier, pour choisir les
// colonnes à la main.
func (s *Service) Head(ctx context.Context, token string) ([]HeadRow, error) {
	if _, err := s.Draft(ctx, token); err != nil {
		return nil, err
	}
	_, head, err := s.inspect(filepath.Join(s.uploads, token))
	return head, err
}

// SetMapping applique une correspondance choisie à la main et, si saveAs
// est renseigné, l'enregistre comme profil pour les imports suivants.
func (s *Service) SetMapping(ctx context.Context, token string, m Mapping, saveAs string) error {
	if err := m.validate(); err != nil {
		return err
	}
	d, err := s.Draft(ctx, token)
	if err != nil {
		return err
	}
	profile := "Correspondance manuelle"
	if strings.TrimSpace(saveAs) != "" {
		head, err := s.Head(ctx, token)
		if err != nil {
			return err
		}
		i := slices.IndexFunc(head, func(r HeadRow) bool { return r.Line == m.HeaderLine })
		if i < 0 {
			return validation.Error("Un profil se reconnaît à sa ligne d'en-tête : indiquez-la pour l'enregistrer.")
		}
		p, err := SaveProfile(s.profilesDir, saveAs, head[i].Cells, m)
		if err != nil {
			return err
		}
		profile = p.Name
	}
	return s.stage(ctx, token, d.AccountID, d.Format, profile, m)
}

// stage interprète tout le fichier selon la correspondance et met ses
// lignes en attente, par lots, dans une seule transaction. Les doublons
// (déjà importés sur ce compte) et les erreurs sont décochés.
func (s *Service) stage(ctx context.Context, token string, accountID int64, format Format, profile string, m Mapping) error {
	mapping, err := json.Marshal(m)
	if err != nil {
		return err
	}
	defer s.db.ReleaseMemory(ctx)
	spreadsheet := format != FormatCSV
	ranks := map[string]int{}
	lines := 0
	return s.db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM import_lines WHERE token = ?`, token); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE import_drafts SET profile = ?, mapping = ? WHERE token = ?`, profile, string(mapping), token); err != nil {
			return err
		}
		batch := make([]any, 0, stageBatch*7)
		flush := func() error {
			if len(batch) == 0 {
				return nil
			}
			n := len(batch) / 7
			query := `INSERT INTO import_lines (token, line, date, label, amount, hash, status, error, selected) VALUES ` + //nolint:gosec // G202 : fragments constants.
				strings.TrimSuffix(strings.Repeat(`(?, ?, ?, ?, ?, ?, ?, ?, 1),`, n), ",")
			args := make([]any, 0, n*8)
			for i := 0; i < len(batch); i += 7 {
				args = append(args, token)
				args = append(args, batch[i:i+7]...)
			}
			_, err := tx.ExecContext(ctx, query, args...)
			batch = batch[:0]
			return err
		}
		err := readRows(filepath.Join(s.uploads, token), format, func(n int, cells []string) error {
			if n <= m.HeaderLine {
				return nil
			}
			l, ok := m.parse(n, cells, spreadsheet)
			if !ok {
				return nil
			}
			lines++
			status, hash := StatusError, ""
			if l.Err == "" {
				l.Label = truncate(l.Label, labelMaxLen)
				key := dedupKey(l)
				ranks[key]++
				status, hash = StatusNew, lineHash(accountID, key, ranks[key])
			}
			batch = append(batch, n, string(l.Date), l.Label, l.Amount, hash, status, l.Err)
			if len(batch) == stageBatch*7 {
				return flush()
			}
			return nil
		})
		if err != nil {
			return err
		}
		if err := flush(); err != nil {
			return err
		}
		if lines == 0 {
			return validation.Error("Aucune opération trouvée sous la ligne d'en-tête.")
		}
		_, err = tx.ExecContext(ctx, `
			UPDATE import_lines SET status = CASE WHEN status = 'nouvelle' THEN 'doublon' ELSE status END, selected = 0
			WHERE token = ? AND (status = 'erreur' OR hash IN (
				SELECT import_hash FROM transactions WHERE account_id = ? AND import_hash IS NOT NULL))`,
			token, accountID)
		return err
	})
}

func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

// Lines renvoie une page des lignes en attente, filtrées par statut
// (toutes si status est vide), et le nombre total de lignes du filtre.
func (s *Service) Lines(ctx context.Context, token, status string, page int) ([]StagedLine, int, error) {
	where, args := `token = ?`, []any{token}
	if status != "" {
		where += ` AND status = ?`
		args = append(args, status)
	}
	var total int
	if err := s.db.R.QueryRowContext(ctx, `SELECT count(*) FROM import_lines WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	query := `SELECT line, date, label, amount, status, error, selected FROM import_lines WHERE ` + //nolint:gosec // G202 : fragments constants.
		where + ` ORDER BY line LIMIT ? OFFSET ?`
	rows, err := s.db.R.QueryContext(ctx, query, append(args, PageSize, (max(page, 1)-1)*PageSize)...)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()
	var out []StagedLine
	for rows.Next() {
		var l StagedLine
		if err := rows.Scan(&l.Line, &l.Date, &l.Label, &l.Amount, &l.Status, &l.Error, &l.Selected); err != nil {
			return nil, 0, err
		}
		out = append(out, l)
	}
	return out, total, rows.Err()
}

// Select coche, parmi les lignes affichées (shown), celles de checked et
// décoche les autres. Seules les lignes nouvelles peuvent être cochées.
func (s *Service) Select(ctx context.Context, token string, shown, checked []int) error {
	if len(shown) == 0 {
		return nil
	}
	shownJSON, _ := json.Marshal(shown)
	checkedJSON, _ := json.Marshal(checked)
	_, err := s.db.W.ExecContext(ctx, `
		UPDATE import_lines SET selected = (status = 'nouvelle' AND line IN (SELECT value FROM json_each(?)))
		WHERE token = ? AND line IN (SELECT value FROM json_each(?))`,
		string(checkedJSON), token, string(shownJSON))
	return err
}

// SelectAll coche ou décoche toutes les lignes nouvelles.
func (s *Service) SelectAll(ctx context.Context, token string, on bool) error {
	_, err := s.db.W.ExecContext(ctx,
		`UPDATE import_lines SET selected = ? WHERE token = ? AND status = 'nouvelle'`, on, token)
	return err
}

// Commit importe les lignes cochées et clôt le brouillon.
func (s *Service) Commit(ctx context.Context, token string) (Import, error) {
	d, err := s.Draft(ctx, token)
	if err != nil {
		return Import{}, err
	}
	if d.Mapping == nil {
		return Import{}, validation.Error("Indiquez d'abord les colonnes du fichier.")
	}
	if d.Counts.Selected == 0 {
		return Import{}, validation.Error("Aucune ligne cochée : rien à importer.")
	}
	imp := Import{
		AccountID: d.AccountID, AccountName: d.AccountName, Filename: d.Filename, Profile: d.Profile,
		Read: d.Counts.Total(), Errors: d.Counts.Error, Duplicate: d.Counts.Duplicate,
		Skipped: d.Counts.New - d.Counts.Selected, ImportedAt: s.now().UTC(),
	}
	err = s.db.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `
			INSERT INTO imports (account_id, filename, profile, imported_at, lines_read, lines_added, lines_duplicate, lines_error, lines_skipped)
			VALUES (?, ?, ?, ?, ?, 0, ?, ?, ?)`,
			imp.AccountID, imp.Filename, imp.Profile, imp.ImportedAt.Format(time.RFC3339), imp.Read, imp.Duplicate, imp.Errors, imp.Skipped)
		if err != nil {
			return err
		}
		if imp.ID, err = res.LastInsertId(); err != nil {
			return err
		}
		// ON CONFLICT : une ligne importée entre-temps (autre onglet) est
		// comptée comme doublon au lieu d'échouer.
		res, err = tx.ExecContext(ctx, `
			INSERT INTO transactions (account_id, date, label, amount, import_id, import_hash)
			SELECT ?, date, label, amount, ?, hash FROM import_lines
			WHERE token = ? AND status = 'nouvelle' AND selected = 1
			ORDER BY date, line
			ON CONFLICT DO NOTHING`,
			imp.AccountID, imp.ID, token)
		if err != nil {
			return err
		}
		added, err := res.RowsAffected()
		if err != nil {
			return err
		}
		imp.Added = int(added)
		imp.Duplicate += d.Counts.Selected - imp.Added
		if _, err := tx.ExecContext(ctx, `UPDATE imports SET lines_added = ?, lines_duplicate = ? WHERE id = ?`,
			imp.Added, imp.Duplicate, imp.ID); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM import_drafts WHERE token = ?`, token)
		return err
	})
	if err != nil {
		return Import{}, err
	}
	s.db.ReleaseMemory(ctx)
	return imp, s.removeFile(token)
}

// Cancel abandonne un import en cours.
func (s *Service) Cancel(ctx context.Context, token string) error {
	if _, err := s.db.W.ExecContext(ctx, `DELETE FROM import_drafts WHERE token = ?`, token); err != nil {
		return err
	}
	return s.removeFile(token)
}

func (s *Service) removeFile(token string) error {
	err := os.Remove(filepath.Join(s.uploads, filepath.Base(token)))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// purge supprime les imports abandonnés depuis plus de draftTTL ; appelée à
// chaque dépôt, elle évite toute tâche de fond.
func (s *Service) purge(ctx context.Context) error {
	tokens, err := s.expiredDrafts(ctx)
	for _, t := range tokens {
		err = errors.Join(err, s.Cancel(ctx, t))
	}
	return err
}

func (s *Service) expiredDrafts(ctx context.Context) ([]string, error) {
	rows, err := s.db.R.QueryContext(ctx, `SELECT token FROM import_drafts WHERE created_at <= ?`, s.now().Add(-draftTTL).Unix())
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var tokens []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		tokens = append(tokens, t)
	}
	return tokens, rows.Err()
}

// History renvoie les imports validés, du plus récent au plus ancien.
func (s *Service) History(ctx context.Context) ([]Import, error) {
	rows, err := s.db.R.QueryContext(ctx, `
		SELECT i.id, i.account_id, a.name, i.filename, i.profile, i.imported_at, i.lines_read, i.lines_added,
		       i.lines_duplicate, i.lines_error, i.lines_skipped, i.undone_at IS NOT NULL
		FROM imports i JOIN accounts a ON a.id = i.account_id
		ORDER BY i.id DESC LIMIT 200`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Import
	for rows.Next() {
		var i Import
		var at string
		if err := rows.Scan(&i.ID, &i.AccountID, &i.AccountName, &i.Filename, &i.Profile, &at, &i.Read, &i.Added,
			&i.Duplicate, &i.Errors, &i.Skipped, &i.Undone); err != nil {
			return nil, err
		}
		if i.ImportedAt, err = time.Parse(time.RFC3339, at); err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// Undo annule un import : les opérations qu'il a créées sont supprimées.
// Renvoie leur nombre.
func (s *Service) Undo(ctx context.Context, id int64) (int, error) {
	var removed int64
	defer s.db.ReleaseMemory(ctx)
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		var undone bool
		err := tx.QueryRowContext(ctx, `SELECT undone_at IS NOT NULL FROM imports WHERE id = ?`, id).Scan(&undone)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if undone {
			return validation.Error("Cet import a déjà été annulé.")
		}
		res, err := tx.ExecContext(ctx, `DELETE FROM transactions WHERE import_id = ?`, id)
		if err != nil {
			return err
		}
		if removed, err = res.RowsAffected(); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE imports SET undone_at = ? WHERE id = ?`, s.now().UTC().Format(time.RFC3339), id)
		return err
	})
	return int(removed), err
}

func (s *Service) exists(ctx context.Context, query string, args ...any) (bool, error) {
	var ok bool
	err := s.db.R.QueryRowContext(ctx, query, args...).Scan(&ok)
	return ok, err
}
