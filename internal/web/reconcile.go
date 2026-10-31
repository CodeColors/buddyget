package web

import (
	"net/http"
	"net/url"
	"strconv"

	"github.com/codecolors/buddyget/internal/ledger"
	"github.com/codecolors/buddyget/internal/platform/calendar"
	"github.com/codecolors/buddyget/internal/platform/money"
)

// reconcileView est la donnée du rapprochement d'un compte avec la banque
// (troisième étape d'un import, ou accès direct depuis le compte) et de son
// pointage : c'est l'unique mécanisme de vérification.
type reconcileView struct {
	R            ledger.Reconciliation
	Balance      string
	Date         string
	FromImport   bool
	UnclearedURL string
	Error        string
	Rows         []pointageRow // opérations à pointer jusqu'à la date du relevé
	More         int           // opérations à pointer au-delà de la première page
}

func (s *Server) reconcilePage(w http.ResponseWriter, r *http.Request) {
	s.renderReconcile(w, r, http.StatusOK, reconcileView{FromImport: r.URL.Query().Get("import") == "1"})
}

func (s *Server) renderReconcile(w http.ResponseWriter, r *http.Request, status int, v reconcileView) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	rec, err := s.ledger.Reconciliation(r.Context(), id)
	if err != nil {
		s.inputError(w, r, err)
		return
	}
	v.R = rec
	if v.Date == "" {
		v.Date = string(calendar.Today())
	}
	if rec.HasBankBalance {
		if v.Balance == "" {
			v.Balance = formatBalance(rec.BankBalance)
		}
		// Seules comptent les opérations datées du solde initial au relevé.
		from := rec.Account.OpeningDate
		q := url.Values{"compte": {strconv.FormatInt(id, 10)}, "pointage": {"non"}, "du": {string(from)}, "au": {string(rec.BankDate)}}
		v.UnclearedURL = "/operations?" + q.Encode()
		page, err := s.ledger.Search(r.Context(), ledger.Filter{AccountID: id, From: from, To: rec.BankDate, Cleared: ledger.ClearedNo, Sort: ledger.SortDateAsc}, 0)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		back := r.URL.Path
		for _, t := range page.Items {
			v.Rows = append(v.Rows, pointageRow{T: t, R: rec, Back: back})
		}
		v.More = page.Count - len(page.Items)
	}
	s.render(w, r, status, "reconcile", "Rapprochement", v)
}

func (s *Server) saveReconcile(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok || parseForm(w, r) != nil {
		s.notFound(w, r)
		return
	}
	v := reconcileView{Balance: r.PostFormValue("balance"), Date: r.PostFormValue("date"), FromImport: r.PostFormValue("import") == "1"}
	err := func() error {
		balance, err := money.Parse(v.Balance)
		if err != nil {
			return err
		}
		date, err := calendar.Parse(v.Date)
		if err != nil {
			return err
		}
		return s.ledger.SetBankBalance(r.Context(), id, balance, date)
	}()
	if err != nil {
		if msg, ok := s.inputError(w, r, err); ok {
			v.Error = msg
			s.renderReconcile(w, r, http.StatusUnprocessableEntity, v)
		}
		return
	}
	back := "/comptes/" + strconv.FormatInt(id, 10) + "/rapprochement"
	if v.FromImport {
		back += "?import=1"
	}
	s.redirect(w, r, back, "Solde bancaire enregistré.")
}
