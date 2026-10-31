package web

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/codecolors/buddyget/internal/importer"
	"github.com/codecolors/buddyget/internal/ledger"
	"github.com/codecolors/buddyget/internal/platform/calendar"
	"github.com/codecolors/buddyget/internal/platform/validation"
)

// importForm est la donnée de l'étape 1 : choix du fichier et du compte.
type importForm struct {
	Accounts  []ledger.AccountBalance
	AccountID int64
	Pending   []importer.Draft
	MaxMB     int64
	Error     string
}

func (s *Server) importPage(w http.ResponseWriter, r *http.Request) {
	s.renderImportForm(w, r, http.StatusOK, importForm{AccountID: idParam(r.URL.Query().Get("compte"))})
}

func (s *Server) renderImportForm(w http.ResponseWriter, r *http.Request, status int, f importForm) {
	accounts, err := s.ledger.Accounts(r.Context(), calendar.Today())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	for _, a := range accounts {
		if !a.Archived {
			f.Accounts = append(f.Accounts, a)
		}
	}
	if f.Pending, err = s.importer.Pending(r.Context()); err != nil {
		s.serverError(w, r, err)
		return
	}
	f.MaxMB = s.maxUpload >> 20
	s.render(w, r, status, "import", "Importer un relevé", f)
}

// uploadImport lit le formulaire en flux : le compte d'abord, puis le
// fichier, copié directement par le service sans fichier temporaire.
func (s *Server) uploadImport(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, s.maxUpload+64<<10)
	f := importForm{}
	token, err := func() (string, error) {
		mr, err := r.MultipartReader()
		if err != nil {
			return "", err
		}
		for {
			part, err := mr.NextPart()
			if errors.Is(err, io.EOF) {
				return "", errors.New("aucun fichier reçu")
			}
			if err != nil {
				return "", err
			}
			switch part.FormName() {
			case "account":
				v, err := io.ReadAll(io.LimitReader(part, 32))
				if err != nil {
					return "", err
				}
				f.AccountID = idParam(string(v))
			case "file":
				if part.FileName() == "" {
					return "", errors.New("aucun fichier choisi")
				}
				return s.importer.Upload(r.Context(), f.AccountID, part.FileName(), part)
			}
		}
	}()
	if err != nil {
		var tooLarge *http.MaxBytesError
		switch msg, ok := validation.Message(err); {
		case ok:
			f.Error = msg
		case errors.As(err, &tooLarge):
			f.Error = fmt.Sprintf("Le fichier dépasse la taille maximale de %d Mo.", s.maxUpload>>20)
		default:
			slog.WarnContext(r.Context(), "dépôt d'import illisible", "err", err)
			f.Error = "Choisissez un fichier CSV, XLS ou XLSX et le compte qui reçoit les opérations."
		}
		s.renderImportForm(w, r, http.StatusUnprocessableEntity, f)
		return
	}
	http.Redirect(w, r, "/import/"+token, http.StatusSeeOther)
}

// draftView est la donnée de l'aperçu (étape 2).
type draftView struct {
	Draft   importer.Draft
	Status  string // onglet : "" (toutes), nouvelle, doublon, erreur
	Lines   []importer.StagedLine
	Total   int
	Page    int
	PrevURL string
	NextURL string
}

func (s *Server) draft(w http.ResponseWriter, r *http.Request) (importer.Draft, bool) {
	d, err := s.importer.Draft(r.Context(), r.PathValue("token"))
	if errors.Is(err, importer.ErrNotFound) {
		s.redirect(w, r, "/import", "Cet import n'est plus en cours.")
		return d, false
	}
	if err != nil {
		s.serverError(w, r, err)
		return d, false
	}
	return d, true
}

func (s *Server) draftPage(w http.ResponseWriter, r *http.Request) {
	d, ok := s.draft(w, r)
	if !ok {
		return
	}
	if d.Mapping == nil {
		s.renderMapping(w, r, http.StatusOK, d, nil, "", "")
		return
	}
	q := r.URL.Query()
	v := draftView{Draft: d, Status: q.Get("statut"), Page: max(int(idParam(q.Get("page"))), 1)}
	switch v.Status {
	case importer.StatusNew, importer.StatusDuplicate, importer.StatusError:
	default:
		v.Status = ""
	}
	lines, total, err := s.importer.Lines(r.Context(), d.Token, v.Status, v.Page)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	v.Lines, v.Total = lines, total
	if v.Page > 1 {
		v.PrevURL = draftURL(d.Token, v.Status, v.Page-1)
	}
	if v.Page*importer.PageSize < total {
		v.NextURL = draftURL(d.Token, v.Status, v.Page+1)
	}
	s.render(w, r, http.StatusOK, "import_preview", "Vérifier l'import", v)
}

func draftURL(token, status string, page int) string {
	q := url.Values{}
	if status != "" {
		q.Set("statut", status)
	}
	if page > 1 {
		q.Set("page", strconv.Itoa(page))
	}
	if len(q) == 0 {
		return "/import/" + token
	}
	return "/import/" + token + "?" + q.Encode()
}

// mappingView est la donnée du choix manuel des colonnes.
type mappingView struct {
	Draft       importer.Draft
	Head        []importer.HeadRow
	Columns     []string // libellé de chaque colonne : « B — Libellé »
	Mapping     importer.Mapping
	Label2      int
	DebitCredit bool
	DateFormats []string
	ProfileName string
	Error       string
}

func (s *Server) mappingPage(w http.ResponseWriter, r *http.Request) {
	if d, ok := s.draft(w, r); ok {
		s.renderMapping(w, r, http.StatusOK, d, d.Mapping, "", "")
	}
}

func (s *Server) renderMapping(w http.ResponseWriter, r *http.Request, status int, d importer.Draft, m *importer.Mapping, profile, problem string) {
	head, err := s.importer.Head(r.Context(), d.Token)
	if err != nil {
		if msg, ok := s.inputError(w, r, err); ok {
			s.redirect(w, r, "/import", msg)
		}
		return
	}
	v := mappingView{Draft: d, Head: head, ProfileName: profile, Error: problem, Label2: importer.NoColumn}
	if m != nil {
		v.Mapping = *m
	} else {
		v.Mapping = importer.Mapping{Date: 0, Labels: []int{1}, Amount: 2, Debit: importer.NoColumn, Credit: importer.NoColumn}
	}
	if len(v.Mapping.Labels) == 0 {
		v.Mapping.Labels = []int{0}
	}
	if len(v.Mapping.Labels) > 1 {
		v.Label2 = v.Mapping.Labels[1]
	}
	v.DebitCredit = v.Mapping.Amount < 0
	for _, f := range importer.DateFormats {
		v.DateFormats = append(v.DateFormats, f.Label)
	}

	// Intitulés : ceux de la ligne d'en-tête si elle est connue, sinon la
	// première valeur de chaque colonne.
	width, names := 0, []string{}
	for _, row := range head {
		width = max(width, len(row.Cells))
		if row.Line == v.Mapping.HeaderLine || len(names) == 0 {
			names = row.Cells
		}
	}
	for i := range width {
		name := ""
		if i < len(names) {
			name = names[i]
		}
		v.Columns = append(v.Columns, strings.TrimSpace(columnLetter(i)+" — "+truncateRunes(name, 30)))
	}
	s.render(w, r, status, "import_mapping", "Colonnes du fichier", v)
}

// columnChoices est la donnée d'une liste de colonnes à choisir.
type columnChoices struct {
	Columns  []string
	Selected int
	Optional bool // propose « Aucune »
}

func columnChoice(columns []string, selected int, optional bool) columnChoices {
	return columnChoices{columns, selected, optional}
}

// columnLetter numérote les colonnes comme un tableur : A, B… Z, AA.
func columnLetter(i int) string {
	s := ""
	for i++; i > 0; i = (i - 1) / 26 {
		s = string(rune('A'+(i-1)%26)) + s
	}
	return s
}

func truncateRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

func (s *Server) saveMapping(w http.ResponseWriter, r *http.Request) {
	d, ok := s.draft(w, r)
	if !ok {
		return
	}
	if err := parseForm(w, r); err != nil {
		s.renderError(w, r, http.StatusBadRequest, "Formulaire illisible.")
		return
	}
	col := func(field string) int {
		n, err := strconv.Atoi(r.PostFormValue(field))
		if err != nil || n < 0 {
			return importer.NoColumn
		}
		return n
	}
	m := importer.Mapping{
		HeaderLine: max(col("header_line"), 0),
		Date:       col("date"),
		DateFormat: r.PostFormValue("date_format"),
		Invert:     r.PostFormValue("invert") != "",
		Amount:     col("amount"),
		Debit:      importer.NoColumn,
		Credit:     importer.NoColumn,
	}
	for _, f := range []string{"label", "label2"} {
		if c := col(f); c >= 0 && (len(m.Labels) == 0 || m.Labels[0] != c) {
			m.Labels = append(m.Labels, c)
		}
	}
	if r.PostFormValue("amount_mode") == "debit-credit" {
		m.Amount, m.Debit, m.Credit = importer.NoColumn, col("debit"), col("credit")
	}
	profile := r.PostFormValue("profile_name")
	if err := s.importer.SetMapping(r.Context(), d.Token, m, profile); err != nil {
		if msg, ok := s.inputError(w, r, err); ok {
			s.renderMapping(w, r, http.StatusUnprocessableEntity, d, &m, profile, msg)
		}
		return
	}
	msg := ""
	if strings.TrimSpace(profile) != "" {
		msg = "Profil « " + strings.TrimSpace(profile) + " » enregistré : il sera reconnu aux prochains imports."
	}
	s.redirect(w, r, "/import/"+d.Token, msg)
}

func (s *Server) selectLines(w http.ResponseWriter, r *http.Request) {
	d, ok := s.draft(w, r)
	if !ok {
		return
	}
	if err := parseForm(w, r); err != nil {
		s.renderError(w, r, http.StatusBadRequest, "Formulaire illisible.")
		return
	}
	var err error
	switch r.PostFormValue("tout") {
	case "1", "0":
		err = s.importer.SelectAll(r.Context(), d.Token, r.PostFormValue("tout") == "1")
	default:
		err = s.importer.Select(r.Context(), d.Token, intList(r.PostForm["shown"]), intList(r.PostForm["checked"]))
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	page, _ := strconv.Atoi(r.PostFormValue("page"))
	http.Redirect(w, r, draftURL(d.Token, r.PostFormValue("statut"), page), http.StatusSeeOther)
}

func intList(values []string) []int {
	out := make([]int, 0, len(values))
	for _, v := range values {
		if n, err := strconv.Atoi(v); err == nil {
			out = append(out, n)
		}
	}
	return out
}

func (s *Server) commitImport(w http.ResponseWriter, r *http.Request) {
	d, ok := s.draft(w, r)
	if !ok {
		return
	}
	imp, err := s.importer.Commit(r.Context(), d.Token)
	if err != nil {
		s.failed(w, r, err, "/import/"+d.Token)
		return
	}
	slog.InfoContext(r.Context(), "import validé", "import", imp.ID, "lues", imp.Read, "ajoutées", imp.Added, "doublons", imp.Duplicate)
	msg := plural(imp.Added, "opération importée", "opérations importées") + "."
	if imp.Duplicate > 0 {
		msg += " " + plural(imp.Duplicate, "doublon ignoré", "doublons ignorés") + "."
	}
	if n, err := s.transferCount(r); err != nil {
		s.serverError(w, r, err)
		return
	} else if n > 0 {
		msg += " " + plural(n, "virement interne possible", "virements internes possibles") + " à vérifier dans Opérations."
	}
	s.redirect(w, r, fmt.Sprintf("/comptes/%d/rapprochement?import=1", imp.AccountID), msg)
}

func (s *Server) cancelImport(w http.ResponseWriter, r *http.Request) {
	if err := s.importer.Cancel(r.Context(), r.PathValue("token")); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.redirect(w, r, "/import", "Import abandonné.")
}

func (s *Server) importHistory(w http.ResponseWriter, r *http.Request) {
	history, err := s.importer.History(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "import_history", "Historique des imports", history)
}

func (s *Server) undoImport(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	removed, err := s.importer.Undo(r.Context(), id)
	if err != nil {
		s.failed(w, r, err, "/import/historique")
		return
	}
	s.redirect(w, r, "/import/historique", "Import annulé : "+plural(removed, "opération supprimée", "opérations supprimées")+".")
}
