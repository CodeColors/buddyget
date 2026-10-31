package web

import (
	"net/http"
	"strconv"

	"github.com/codecolors/buddyget/internal/ledger"
	"github.com/codecolors/buddyget/internal/platform/calendar"
	"github.com/codecolors/buddyget/internal/platform/money"
)

// accountsView sert au tableau de bord et à la page des comptes.
type accountsView struct {
	Active   []ledger.AccountBalance
	Archived []ledger.AccountBalance
	Total    int64 // somme des soldes des comptes actifs
	Today    calendar.Date
}

func (s *Server) loadAccounts(r *http.Request) (accountsView, error) {
	v := accountsView{Today: calendar.Today()}
	all, err := s.ledger.Accounts(r.Context(), v.Today)
	for _, a := range all {
		if a.Archived {
			v.Archived = append(v.Archived, a)
		} else {
			v.Active = append(v.Active, a)
			v.Total += a.Balance
		}
	}
	return v, err
}

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	v, err := s.loadAccounts(r)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "dashboard", "Tableau de bord", v)
}

func (s *Server) accountsPage(w http.ResponseWriter, r *http.Request) {
	v, err := s.loadAccounts(r)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "accounts", "Comptes", v)
}

// accountForm est la donnée du formulaire de compte, en texte saisi.
type accountForm struct {
	ID             int64
	Archived       bool
	Name           string
	Kind           ledger.AccountKind
	Bank           ledger.Bank
	IBAN           string
	OpeningBalance string
	OpeningDate    string
	Error          string
	Kinds          []ledger.AccountKind
	Banks          []ledger.Bank
}

func newAccountForm() accountForm {
	return accountForm{Kinds: ledger.AccountKinds, Banks: ledger.Banks}
}

func (f accountForm) title() string {
	if f.ID == 0 {
		return "Nouveau compte"
	}
	return "Modifier le compte"
}

func (s *Server) newAccountPage(w http.ResponseWriter, r *http.Request) {
	f := newAccountForm()
	f.Kind, f.Bank = ledger.AccountCurrent, ledger.BankSG
	f.OpeningBalance, f.OpeningDate = "0,00", string(calendar.Today())
	s.render(w, r, http.StatusOK, "account_form", f.title(), f)
}

func (s *Server) editAccountPage(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	a, err := s.ledger.Account(r.Context(), id)
	if err != nil {
		s.inputError(w, r, err)
		return
	}
	f := newAccountForm()
	f.ID, f.Archived, f.Name, f.Kind, f.Bank, f.IBAN = a.ID, a.Archived, a.Name, a.Kind, a.Bank, a.IBAN
	f.OpeningBalance, f.OpeningDate = formatBalance(a.OpeningBalance), string(a.OpeningDate)
	s.render(w, r, http.StatusOK, "account_form", f.title(), f)
}

// formatBalance prépare un solde signé pour un champ de saisie.
func formatBalance(cents int64) string {
	if cents < 0 {
		return "-" + money.FormatInput(cents)
	}
	return money.FormatInput(cents)
}

func (s *Server) createAccount(w http.ResponseWriter, r *http.Request) {
	s.saveAccount(w, r, 0)
}

func (s *Server) updateAccount(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	s.saveAccount(w, r, id)
}

func (s *Server) saveAccount(w http.ResponseWriter, r *http.Request, id int64) {
	if err := parseForm(w, r); err != nil {
		s.renderError(w, r, http.StatusBadRequest, "Formulaire illisible.")
		return
	}
	f := newAccountForm()
	f.ID = id
	f.Name, f.IBAN = r.PostFormValue("name"), r.PostFormValue("iban")
	f.Kind, f.Bank = ledger.AccountKind(r.PostFormValue("kind")), ledger.Bank(r.PostFormValue("bank"))
	f.OpeningBalance, f.OpeningDate = r.PostFormValue("opening_balance"), r.PostFormValue("opening_date")

	err := func() error {
		balance, err := money.Parse(f.OpeningBalance)
		if err != nil {
			return err
		}
		date, err := calendar.Parse(f.OpeningDate)
		if err != nil {
			return err
		}
		in := ledger.AccountInput{Name: f.Name, Kind: f.Kind, Bank: f.Bank, IBAN: f.IBAN, OpeningBalance: balance, OpeningDate: date}
		if id == 0 {
			_, err = s.ledger.CreateAccount(r.Context(), in)
			return err
		}
		return s.ledger.UpdateAccount(r.Context(), id, in)
	}()
	if err != nil {
		if msg, ok := s.inputError(w, r, err); ok {
			f.Error = msg
			s.render(w, r, http.StatusUnprocessableEntity, "account_form", f.title(), f)
		}
		return
	}
	s.redirect(w, r, "/comptes", "Compte enregistré.")
}

func (s *Server) archiveAccount(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok || parseForm(w, r) != nil {
		s.notFound(w, r)
		return
	}
	archived, _ := strconv.ParseBool(r.PostFormValue("archived"))
	if err := s.ledger.SetAccountArchived(r.Context(), id, archived); err != nil {
		s.failed(w, r, err, "/comptes")
		return
	}
	msg := "Compte réactivé."
	if archived {
		msg = "Compte archivé."
	}
	s.redirect(w, r, "/comptes", msg)
}

func (s *Server) deleteAccount(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	if err := s.ledger.DeleteAccount(r.Context(), id); err != nil {
		s.failed(w, r, err, "/comptes/"+strconv.FormatInt(id, 10))
		return
	}
	s.redirect(w, r, "/comptes", "Compte supprimé.")
}
