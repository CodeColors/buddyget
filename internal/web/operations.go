package web

import (
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/codecolors/buddyget/internal/ledger"
	"github.com/codecolors/buddyget/internal/platform/calendar"
	"github.com/codecolors/buddyget/internal/platform/money"
)

// Types d'opération proposés à la saisie.
const (
	typeExpense  = "depense"
	typeIncome   = "revenu"
	typeTransfer = "virement"
)

// parseFilter lit les critères de recherche de l'URL. Un critère invalide
// est ignoré et signalé par un message.
func parseFilter(q url.Values) (ledger.Filter, string) {
	f := ledger.Filter{
		Query:     q.Get("q"),
		AccountID: idParam(q.Get("compte")),
		Cleared:   ledger.Cleared(q.Get("pointage")),
		Sort:      ledger.Sort(q.Get("tri")),
	}
	if c := q.Get("categorie"); c == "aucune" {
		f.CategoryID = ledger.Uncategorized
	} else {
		f.CategoryID = idParam(c)
	}
	var problem string
	for _, d := range []struct {
		param string
		dst   *calendar.Date
	}{{"du", &f.From}, {"au", &f.To}} {
		if v := q.Get(d.param); v != "" {
			date, err := calendar.Parse(v)
			if err != nil {
				problem = "Une date de la recherche est invalide : elle est ignorée."
			}
			*d.dst = date
		}
	}
	for _, a := range []struct {
		param string
		dst   *int64
	}{{"min", &f.MinAmount}, {"max", &f.MaxAmount}} {
		if v := q.Get(a.param); v != "" {
			amount, err := money.Parse(v)
			if err != nil {
				problem = "Un montant de la recherche est invalide : il est ignoré."
			}
			*a.dst = max(amount, -amount)
		}
	}
	return f, problem
}

func idParam(v string) int64 {
	id, _ := strconv.ParseInt(v, 10, 64)
	return max(id, 0)
}

// operationsView est la donnée de la liste des opérations.
type operationsView struct {
	Q          url.Values // critères saisis, pour remplir le formulaire
	Filter     string     // critères encodés, réutilisés par l'édition par lot
	Problem    string
	Page       ledger.Page
	PageNumber int
	PrevURL    string
	NextURL    string
	Accounts   []ledger.AccountBalance
	Categories []ledger.KindGroup
	Today      calendar.Date
}

// Searching indique qu'au moins un critère de recherche est renseigné.
func (v operationsView) Searching() bool {
	for k, vals := range v.Q {
		if k != "page" && slices.ContainsFunc(vals, func(s string) bool { return s != "" }) {
			return true
		}
	}
	return false
}

// FiltersOpen indique qu'un critère du panneau de filtres est actif, pour
// l'afficher déplié.
func (v operationsView) FiltersOpen() bool {
	for _, k := range []string{"compte", "categorie", "du", "au", "min", "max", "pointage", "tri"} {
		if v.Q.Get(k) != "" {
			return true
		}
	}
	return false
}

func (s *Server) operationsPage(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f, problem := parseFilter(q)
	pageNumber := max(int(idParam(q.Get("page"))), 1)
	page, err := s.ledger.Search(r.Context(), f, (pageNumber-1)*ledger.PageSize)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	v := operationsView{Q: q, Problem: problem, Page: page, PageNumber: pageNumber, Today: calendar.Today()}
	q.Del("page")
	v.Filter = q.Encode()
	if page.HasPrev() {
		v.PrevURL = pageURL(q, pageNumber-1)
	}
	if page.HasNext() {
		v.NextURL = pageURL(q, pageNumber+1)
	}

	// Les mises à jour HTMX de la recherche ne renvoient que les résultats.
	w.Header().Set("Vary", "HX-Request")
	if r.Header.Get("HX-Request") == "true" && r.Header.Get("HX-Target") == "resultats" {
		s.renderBlock(w, r, "operations", "results", v)
		return
	}
	if v.Accounts, err = s.ledger.Accounts(r.Context(), v.Today); err != nil {
		s.serverError(w, r, err)
		return
	}
	cats, err := s.ledger.Categories(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	v.Categories = ledger.GroupByKind(cats)
	s.render(w, r, http.StatusOK, "operations", "Opérations", v)
}

func pageURL(q url.Values, page int) string {
	q = cloneValues(q)
	if page > 1 {
		q.Set("page", strconv.Itoa(page))
	}
	if len(q) == 0 {
		return "/operations"
	}
	return "/operations?" + q.Encode()
}

func cloneValues(q url.Values) url.Values {
	out := make(url.Values, len(q))
	for k, v := range q {
		out[k] = append([]string(nil), v...)
	}
	return out
}

// operationForm est la donnée du formulaire d'opération, en texte saisi.
type operationForm struct {
	ID          int64
	TransferID  int64
	Type        string
	Amount      string
	Label       string
	Date        string
	AccountID   int64
	ToAccountID int64
	CategoryID  int64
	Note        string
	Cleared     bool
	Back        string // page de retour après enregistrement
	Error       string

	Accounts   []ledger.AccountBalance
	Categories []ledger.KindGroup
	Labels     []string
}

func (f operationForm) title() string {
	switch {
	case f.ID == 0:
		return "Nouvelle opération"
	case f.TransferID != 0:
		return "Modifier le virement"
	}
	return "Modifier l'opération"
}

// withOptions charge les listes du formulaire : comptes actifs (et le compte
// de l'opération, même archivé), catégories, libellés récents.
func (s *Server) withOptions(r *http.Request, f operationForm) (operationForm, error) {
	ctx := r.Context()
	accounts, err := s.ledger.Accounts(ctx, calendar.Today())
	if err != nil {
		return f, err
	}
	for _, a := range accounts {
		if !a.Archived || a.ID == f.AccountID || a.ID == f.ToAccountID {
			f.Accounts = append(f.Accounts, a)
		}
	}
	cats, err := s.ledger.Categories(ctx)
	if err != nil {
		return f, err
	}
	f.Categories = ledger.GroupByKind(cats)
	f.Labels, err = s.ledger.RecentLabels(ctx, 50)
	return f, err
}

func (s *Server) renderOperationForm(w http.ResponseWriter, r *http.Request, status int, f operationForm) {
	f, err := s.withOptions(r, f)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, status, "operation_form", f.title(), f)
}

func (s *Server) newOperationPage(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := operationForm{
		Type:      q.Get("type"),
		Date:      q.Get("date"),
		AccountID: idParam(q.Get("compte")),
		Back:      q.Get("retour"),
	}
	if f.Back == "" {
		f.Back = backFromReferer(r)
	}
	f.Back = localPath(f.Back)
	if f.Type != typeIncome && f.Type != typeTransfer {
		f.Type = typeExpense
	}
	if _, err := calendar.Parse(f.Date); err != nil {
		f.Date = string(calendar.Today())
	}
	if f.AccountID == 0 {
		id, err := s.ledger.LastUsedAccount(r.Context())
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		f.AccountID = id
	}
	s.renderOperationForm(w, r, http.StatusOK, f)
}

func (s *Server) editOperationPage(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	t, err := s.ledger.Transaction(r.Context(), id)
	if err != nil {
		s.inputError(w, r, err)
		return
	}
	f := operationForm{
		ID: t.ID, TransferID: t.TransferID, Label: t.Label, Date: string(t.Date),
		AccountID: t.AccountID, CategoryID: t.CategoryID, Note: t.Note, Cleared: t.Cleared,
		Amount: money.FormatInput(t.Amount), Back: backFromReferer(r),
	}
	switch {
	case t.IsTransfer() && t.Amount < 0:
		f.Type, f.ToAccountID = typeTransfer, t.CounterpartAccountID
	case t.IsTransfer():
		f.Type, f.AccountID, f.ToAccountID = typeTransfer, t.CounterpartAccountID, t.AccountID
	case t.Amount > 0:
		f.Type = typeIncome
	default:
		f.Type = typeExpense
	}
	s.renderOperationForm(w, r, http.StatusOK, f)
}

// backFromReferer retient la page d'où vient l'utilisateur (par exemple la
// liste filtrée), pour l'y ramener après l'enregistrement.
func backFromReferer(r *http.Request) string {
	ref, err := url.Parse(r.Referer())
	if err != nil || ref.Host != r.Host || strings.HasPrefix(ref.Path, "/operations/") {
		return "/operations"
	}
	return ref.RequestURI()
}

func (s *Server) createOperation(w http.ResponseWriter, r *http.Request) {
	s.saveOperation(w, r, 0)
}

func (s *Server) updateOperation(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	s.saveOperation(w, r, id)
}

func (s *Server) saveOperation(w http.ResponseWriter, r *http.Request, id int64) {
	if err := parseForm(w, r); err != nil {
		s.renderError(w, r, http.StatusBadRequest, "Formulaire illisible.")
		return
	}
	f := operationForm{
		ID:          id,
		Type:        r.PostFormValue("type"),
		Amount:      r.PostFormValue("amount"),
		Label:       r.PostFormValue("label"),
		Date:        r.PostFormValue("date"),
		AccountID:   formID(r, "account"),
		ToAccountID: formID(r, "to_account"),
		CategoryID:  formID(r, "category"),
		Note:        r.PostFormValue("note"),
		Cleared:     r.PostFormValue("cleared") != "",
		Back:        localPath(r.PostFormValue("back")),
	}
	// La nature d'une opération existante (simple ou virement) ne change pas.
	if id != 0 {
		t, err := s.ledger.Transaction(r.Context(), id)
		if err != nil {
			s.inputError(w, r, err)
			return
		}
		f.TransferID = t.TransferID
		switch {
		case t.IsTransfer():
			f.Type = typeTransfer
		case f.Type == typeTransfer:
			f.Type = typeExpense
		}
	}
	if err := s.storeOperation(r, f); err != nil {
		if msg, ok := s.inputError(w, r, err); ok {
			f.Error = msg
			s.renderOperationForm(w, r, http.StatusUnprocessableEntity, f)
		}
		return
	}

	msg := "Opération enregistrée."
	if f.Type == typeTransfer {
		msg = "Virement enregistré."
	}
	if r.PostFormValue("next") == "nouvelle" {
		next := url.Values{"type": {f.Type}, "date": {f.Date}, "compte": {strconv.FormatInt(f.AccountID, 10)}, "retour": {f.Back}}
		s.redirect(w, r, "/operations/nouvelle?"+next.Encode(), msg)
		return
	}
	s.redirect(w, r, f.Back, msg)
}

// storeOperation convertit la saisie et l'enregistre (création ou
// modification, opération simple ou virement).
func (s *Server) storeOperation(r *http.Request, f operationForm) error {
	amount, err := money.Parse(f.Amount)
	if err != nil {
		return err
	}
	amount = max(amount, -amount)
	date, err := calendar.Parse(f.Date)
	if err != nil {
		return err
	}
	ctx := r.Context()

	if f.Type == typeTransfer {
		in := ledger.TransferInput{FromAccountID: f.AccountID, ToAccountID: f.ToAccountID, Date: date, Label: f.Label, Amount: amount, Note: f.Note}
		if f.ID == 0 {
			_, err = s.ledger.CreateTransfer(ctx, in)
			return err
		}
		return s.ledger.UpdateTransfer(ctx, f.TransferID, in)
	}
	if f.Type == typeExpense {
		amount = -amount
	}
	in := ledger.TransactionInput{AccountID: f.AccountID, Date: date, Label: f.Label, Amount: amount, CategoryID: f.CategoryID, Note: f.Note, Cleared: f.Cleared}
	if f.ID == 0 {
		_, err = s.ledger.CreateTransaction(ctx, in)
		return err
	}
	return s.ledger.UpdateTransaction(ctx, f.ID, in)
}

func (s *Server) deleteOperation(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok || parseForm(w, r) != nil {
		s.notFound(w, r)
		return
	}
	back := localPath(r.PostFormValue("back"))
	if err := s.ledger.DeleteTransaction(r.Context(), id); err != nil {
		s.failed(w, r, err, back)
		return
	}
	s.redirect(w, r, back, "Opération supprimée.")
}

// batchOperations applique une édition par lot aux opérations cochées ou à
// tous les résultats de la recherche courante.
func (s *Server) batchOperations(w http.ResponseWriter, r *http.Request) {
	if err := parseForm(w, r); err != nil {
		s.renderError(w, r, http.StatusBadRequest, "Formulaire illisible.")
		return
	}
	query, _ := url.ParseQuery(r.PostFormValue("filtre"))
	back := pageURL(query, 1)

	var sel ledger.Selection
	if r.PostFormValue("portee") == "tout" {
		f, _ := parseFilter(query)
		sel.Filter = &f
	} else {
		for _, v := range r.PostForm["ids"] {
			if id := idParam(v); id != 0 {
				sel.IDs = append(sel.IDs, id)
			}
		}
	}
	var ch ledger.BatchChange
	switch c := r.PostFormValue("categorie"); c {
	case "":
	case "aucune":
		ch.SetCategory = true
	default:
		ch.SetCategory, ch.CategoryID = true, idParam(c)
	}
	ch.AccountID = idParam(r.PostFormValue("compte"))

	res, err := s.ledger.BatchUpdate(r.Context(), sel, ch)
	if err != nil {
		s.failed(w, r, err, back)
		return
	}
	msg := plural(res.Updated, "opération modifiée", "opérations modifiées") + "."
	switch n := res.SkippedTransfers; {
	case n == 1:
		msg += " 1 virement laissé tel quel."
	case n > 1:
		msg += fmt.Sprintf(" %d virements laissés tels quels.", n)
	}
	s.redirect(w, r, back, msg)
}
