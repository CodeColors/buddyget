package web

import (
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/codecolors/buddyget/internal/ledger"
	"github.com/codecolors/buddyget/internal/loans"
	"github.com/codecolors/buddyget/internal/platform/calendar"
	"github.com/codecolors/buddyget/internal/platform/money"
	"github.com/codecolors/buddyget/internal/platform/validation"
)

// parseRate lit un taux annuel en pourcentage, au millième de point
// (« 3,45 », « 3,125 % ») ; vide vaut 0.
func parseRate(s string) (int, error) {
	s = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "%"))
	if s == "" {
		return 0, nil
	}
	v, err := strconv.ParseFloat(strings.Replace(s, ",", ".", 1), 64)
	if err != nil || math.IsNaN(v) || v < 0 || v > 100 {
		return 0, validation.Error("Taux invalide : saisissez un pourcentage, par exemple 3,45.")
	}
	return int(math.Round(v * 1000)), nil
}

// formatRate écrit un taux en millièmes de point : « 3,45 », « 3,125 ».
func formatRate(thousandths int) string {
	if frac := thousandths % 1000; frac%10 != 0 {
		return fmt.Sprintf("%d,%03d", thousandths/1000, frac)
	}
	return fmt.Sprintf("%d,%02d", thousandths/1000, thousandths%1000/10)
}

// loanSummary est un crédit dans la liste : sa situation au jour.
type loanSummary struct {
	loans.Loan
	Schedule loans.Schedule
	Status   loans.Status
}

// Repaid renvoie la part du capital remboursée, de 0 à 100.
func (l loanSummary) Repaid() int {
	return int((l.Principal - l.Status.Remaining) * 100 / l.Principal)
}

// Monthly renvoie la mensualité, assurance comprise.
func (l loanSummary) Monthly() int64 { return l.Schedule.Payment + l.Insurance }

func summarize(l loans.Loan, today calendar.Date) loanSummary {
	sched := l.Schedule()
	return loanSummary{Loan: l, Schedule: sched, Status: sched.StatusAt(l.Principal, today)}
}

func (s *Server) loansPage(w http.ResponseWriter, r *http.Request) {
	list, err := s.loans.Loans(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	today := calendar.Today()
	out := make([]loanSummary, len(list))
	for i, l := range list {
		out[i] = summarize(l, today)
	}
	s.render(w, r, http.StatusOK, "loans", "Crédits", out)
}

// loanYear regroupe les échéances d'une année du tableau d'amortissement.
type loanYear struct {
	Year                      int
	Rows                      []loans.Row
	Interest, Capital, Prepay int64
	Remaining                 int64
	Current                   bool
}

// loanView est la donnée de la fiche d'un crédit.
type loanView struct {
	loanSummary
	Years  []loanYear
	Today  calendar.Date
	Prepay prepayForm
}

// prepayForm est la saisie d'un remboursement anticipé.
type prepayForm struct {
	Date, Amount, Error string
}

func years(sched loans.Schedule, today calendar.Date) []loanYear {
	var out []loanYear
	for _, row := range sched.Rows {
		y := row.Date.Time().Year()
		if n := len(out); n == 0 || out[n-1].Year != y {
			out = append(out, loanYear{Year: y, Current: y == today.Time().Year()})
		}
		ly := &out[len(out)-1]
		ly.Rows = append(ly.Rows, row)
		ly.Interest += row.Interest
		ly.Capital += row.Capital
		ly.Prepay += row.Prepaid
		ly.Remaining = row.Remaining
	}
	return out
}

func (s *Server) loanPage(w http.ResponseWriter, r *http.Request) {
	s.renderLoan(w, r, http.StatusOK, prepayForm{Date: string(calendar.Today())})
}

func (s *Server) renderLoan(w http.ResponseWriter, r *http.Request, status int, f prepayForm) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	l, err := s.loans.Loan(r.Context(), id)
	if err != nil {
		s.inputError(w, r, err)
		return
	}
	today := calendar.Today()
	v := loanView{loanSummary: summarize(l, today), Today: today, Prepay: f}
	v.Years = years(v.Schedule, today)
	s.render(w, r, status, "loan", "Crédit "+l.Name, v)
}

func (s *Server) prepayLoan(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	if parseForm(w, r) != nil {
		s.renderError(w, r, http.StatusBadRequest, "Formulaire illisible.")
		return
	}
	f := prepayForm{Date: r.PostFormValue("date"), Amount: r.PostFormValue("amount")}
	err := func() error {
		date, err := calendar.Parse(f.Date)
		if err != nil {
			return err
		}
		amount, err := money.Parse(f.Amount)
		if err != nil {
			return err
		}
		return s.loans.Prepay(r.Context(), id, loans.Prepayment{Date: date, Amount: amount})
	}()
	if err != nil {
		if msg, ok := s.inputError(w, r, err); ok {
			f.Error = msg
			s.renderLoan(w, r, http.StatusUnprocessableEntity, f)
		}
		return
	}
	s.redirect(w, r, fmt.Sprintf("/credits/%d", id), "Remboursement anticipé enregistré : la mensualité s'arrête plus tôt.")
}

// loanForm est la donnée du formulaire de crédit, en texte saisi.
type loanForm struct {
	ID        int64
	Name      string
	AccountID int64
	Principal string
	Rate      string
	Months    string
	Insurance string
	First     string
	Error     string
	Accounts  []ledger.AccountBalance
}

func (s *Server) renderLoanForm(w http.ResponseWriter, r *http.Request, status int, f loanForm) {
	accounts, err := s.ledger.Accounts(r.Context(), calendar.Today())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	for _, a := range accounts {
		if !a.Archived || a.ID == f.AccountID {
			f.Accounts = append(f.Accounts, a)
		}
	}
	title := "Nouveau crédit"
	if f.ID != 0 {
		title = "Modifier le crédit"
	}
	s.render(w, r, status, "loan_form", title, f)
}

func (s *Server) newLoanPage(w http.ResponseWriter, r *http.Request) {
	s.renderLoanForm(w, r, http.StatusOK, loanForm{First: string(calendar.Today())})
}

func (s *Server) editLoanPage(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	l, err := s.loans.Loan(r.Context(), id)
	if err != nil {
		s.inputError(w, r, err)
		return
	}
	f := loanForm{
		ID: id, Name: l.Name, AccountID: l.AccountID, Principal: money.FormatInput(l.Principal), Rate: formatRate(l.Rate),
		Months: strconv.Itoa(l.Months), Insurance: money.FormatInput(l.Insurance), First: string(l.First),
	}
	s.renderLoanForm(w, r, http.StatusOK, f)
}

func (s *Server) createLoan(w http.ResponseWriter, r *http.Request) { s.saveLoan(w, r, 0) }

func (s *Server) updateLoan(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	s.saveLoan(w, r, id)
}

func (s *Server) saveLoan(w http.ResponseWriter, r *http.Request, id int64) {
	if parseForm(w, r) != nil {
		s.renderError(w, r, http.StatusBadRequest, "Formulaire illisible.")
		return
	}
	f := loanForm{
		ID: id, Name: r.PostFormValue("name"), AccountID: formID(r, "account"), Principal: r.PostFormValue("principal"),
		Rate: r.PostFormValue("rate"), Months: r.PostFormValue("months"), Insurance: r.PostFormValue("insurance"), First: r.PostFormValue("first"),
	}
	in, err := f.input()
	if err == nil {
		if id == 0 {
			id, err = s.loans.Create(r.Context(), in)
		} else {
			err = s.loans.Update(r.Context(), id, in)
		}
	}
	if err != nil {
		if msg, ok := s.inputError(w, r, err); ok {
			f.Error = msg
			s.renderLoanForm(w, r, http.StatusUnprocessableEntity, f)
		}
		return
	}
	s.redirect(w, r, fmt.Sprintf("/credits/%d", id), "Crédit enregistré, mensualité ajoutée aux récurrences.")
}

// input lit la saisie ; la validation métier revient au service.
func (f loanForm) input() (loans.Input, error) {
	in := loans.Input{Name: f.Name, AccountID: f.AccountID}
	var err error
	if in.Principal, err = money.Parse(f.Principal); err != nil {
		return in, err
	}
	if in.Rate, err = parseRate(f.Rate); err != nil {
		return in, err
	}
	if in.Months, err = strconv.Atoi(strings.TrimSpace(f.Months)); err != nil {
		return in, validation.Error("Indiquez la durée en mois.")
	}
	if strings.TrimSpace(f.Insurance) != "" {
		if in.Insurance, err = money.Parse(f.Insurance); err != nil {
			return in, err
		}
	}
	in.First, err = calendar.Parse(f.First)
	return in, err
}

func (s *Server) deleteLoan(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	if err := s.loans.Delete(r.Context(), id); err != nil {
		s.failed(w, r, err, "/credits")
		return
	}
	s.redirect(w, r, "/credits", "Crédit supprimé, ainsi que sa mensualité.")
}
