package web

import (
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/codecolors/buddyget/internal/alerts"
	"github.com/codecolors/buddyget/internal/forecast"
	"github.com/codecolors/buddyget/internal/ledger"
	"github.com/codecolors/buddyget/internal/platform/calendar"
	"github.com/codecolors/buddyget/internal/platform/money"
	"github.com/codecolors/buddyget/internal/platform/validation"
	"github.com/codecolors/buddyget/internal/savings"
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

// dashboardShown borne les enveloppes et alertes du tableau de bord.
const dashboardShown = 3

// dashboardView est la synthèse du tableau de bord.
type dashboardView struct {
	accountsView
	Outlook    forecast.Outlook
	HasOutlook bool
	Envelopes  []envelope // les plus consommées
	Alerts     []alerts.Alert
}

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	var v dashboardView
	var err error
	if v.accountsView, err = s.loadAccounts(r); err != nil {
		s.serverError(w, r, err)
		return
	}
	if v.Outlook, v.HasOutlook, err = s.forecast.Outlook(r.Context(), v.Today); err != nil {
		s.serverError(w, r, err)
		return
	}
	if v.Envelopes, err = s.envelopes(r, v.Today); err != nil {
		s.serverError(w, r, err)
		return
	}
	slices.SortStableFunc(v.Envelopes, func(a, b envelope) int { return b.Percent() - a.Percent() })
	v.Envelopes = v.Envelopes[:min(len(v.Envelopes), dashboardShown)]
	if v.Alerts, err = s.alerts.Open(r.Context()); err != nil {
		s.serverError(w, r, err)
		return
	}
	v.Alerts = v.Alerts[:min(len(v.Alerts), dashboardShown)]
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
	Savings        savingsForm
	Error          string
	Kinds          []ledger.AccountKind
	Banks          []ledger.Bank
}

// savingsForm est la partie « épargne » du formulaire de compte.
type savingsForm struct {
	Product      savings.Product
	Rate         string
	Method       savings.Method
	AutoInterest bool
	Ceiling      string
	Lock         string // aucun, date ou duree
	UnlockDate   string
	LockMonths   string
	Penalty      string
	// Modèle choisi à la création : ses valeurs sont à vérifier.
	Template *savings.Template
}

// Choix de blocage des fonds.
const (
	lockNone    = "aucun"
	lockByDate  = "date"
	lockRolling = "duree"
)

func savingsFormFrom(st savings.Settings) savingsForm {
	f := savingsForm{Product: st.Product, Method: st.Method, AutoInterest: st.AutoInterest, Lock: lockNone, UnlockDate: string(st.UnlockDate)}
	if st.Rate > 0 {
		f.Rate = money.FormatInput(int64(st.Rate))
	}
	if st.Ceiling > 0 {
		f.Ceiling = money.FormatInput(st.Ceiling)
	}
	if st.Penalty > 0 {
		f.Penalty = money.FormatInput(int64(st.Penalty))
	}
	switch {
	case st.UnlockDate != "":
		f.Lock = lockByDate
	case st.LockMonths > 0:
		f.Lock, f.LockMonths = lockRolling, strconv.Itoa(st.LockMonths)
	}
	return f
}

// settings convertit la saisie ; les champs vides valent « sans ».
func (f savingsForm) settings() (savings.Settings, error) {
	st := savings.Settings{Product: f.Product, Method: f.Method, AutoInterest: f.AutoInterest}
	var err error
	if st.Rate, err = parsePercent(f.Rate, "Taux"); err != nil {
		return st, err
	}
	if st.Penalty, err = parsePercent(f.Penalty, "Pénalité"); err != nil {
		return st, err
	}
	if strings.TrimSpace(f.Ceiling) != "" {
		if st.Ceiling, err = money.Parse(f.Ceiling); err != nil {
			return st, err
		}
	}
	switch f.Lock {
	case lockByDate:
		if st.UnlockDate, err = calendar.Parse(f.UnlockDate); err != nil {
			return st, validation.Error("Indiquez la date de déblocage des fonds.")
		}
	case lockRolling:
		if st.LockMonths, err = strconv.Atoi(f.LockMonths); err != nil || st.LockMonths < 1 {
			return st, validation.Error("Indiquez la durée de blocage en mois.")
		}
	}
	return st, st.Validate()
}

// parsePercent lit un pourcentage à deux décimales en centièmes de point.
func parsePercent(s, field string) (int, error) {
	if strings.TrimSpace(s) == "" {
		return 0, nil
	}
	v, err := money.Parse(s)
	if err != nil || v < 0 || v > 100_00 {
		return 0, validation.Error(field + " invalide : saisissez un pourcentage, par exemple 1,70.")
	}
	return int(v), nil
}

func newAccountForm() accountForm {
	return accountForm{Kinds: ledger.AccountKinds, Banks: ledger.Banks}
}

// SavingsTemplates, SavingsMethods… exposent les choix au gabarit.
func (accountForm) SavingsTemplates() []savings.Template { return savings.Templates }

func (accountForm) SavingsMethods() []savings.Method { return savings.Methods }

func (accountForm) TemplatesCheckedOn() calendar.Date { return savings.TemplatesCheckedOn }

func (accountForm) RatesSince() calendar.Date { return savings.RatesSince }

func (f accountForm) title() string {
	if f.ID == 0 {
		return "Nouveau compte"
	}
	return "Modifier le compte"
}

// newAccountPage ouvre un formulaire vierge, ou prérempli d'après un modèle
// d'épargne (?modele=livret_a).
func (s *Server) newAccountPage(w http.ResponseWriter, r *http.Request) {
	f := newAccountForm()
	f.Kind, f.Bank = ledger.AccountCurrent, ledger.BankSG
	f.OpeningBalance, f.OpeningDate = "0,00", string(calendar.Today())
	f.Savings = savingsFormFrom(savings.Settings{Product: savings.OtherProduct, Method: savings.NoInterest, AutoInterest: true})
	if t, ok := savings.TemplateFor(savings.Product(r.URL.Query().Get("modele"))); ok {
		f.Name, f.Kind, f.Bank = t.Label, ledger.AccountSavings, ledger.BankOther
		f.Savings = savingsFormFrom(t.Settings())
		f.Savings.Template = &t
	}
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
	st, ok, err := s.savings.Settings(r.Context(), id)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if !ok {
		st = savings.Settings{Product: savings.OtherProduct, Method: savings.NoInterest, AutoInterest: true}
	}
	f.Savings = savingsFormFrom(st)
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
	f.Savings = savingsForm{
		Product: savings.Product(r.PostFormValue("product")), Method: savings.Method(r.PostFormValue("method")),
		Rate: r.PostFormValue("rate"), Ceiling: r.PostFormValue("ceiling"), Penalty: r.PostFormValue("penalty"),
		AutoInterest: r.PostFormValue("auto_interest") != "", Lock: r.PostFormValue("lock"),
		UnlockDate: r.PostFormValue("unlock_date"), LockMonths: r.PostFormValue("lock_months"),
	}

	err := func() error {
		balance, err := money.Parse(f.OpeningBalance)
		if err != nil {
			return err
		}
		date, err := calendar.Parse(f.OpeningDate)
		if err != nil {
			return err
		}
		// Les réglages d'épargne sont vérifiés avant d'écrire quoi que ce soit.
		var st savings.Settings
		if f.Kind == ledger.AccountSavings {
			if st, err = f.Savings.settings(); err != nil {
				return err
			}
		}
		in := ledger.AccountInput{Name: f.Name, Kind: f.Kind, Bank: f.Bank, IBAN: f.IBAN, OpeningBalance: balance, OpeningDate: date}
		if id == 0 {
			id, err = s.ledger.CreateAccount(r.Context(), in)
		} else {
			err = s.ledger.UpdateAccount(r.Context(), id, in)
		}
		if err != nil {
			return err
		}
		if f.Kind != ledger.AccountSavings {
			return s.savings.Remove(r.Context(), id)
		}
		st.AccountID = id
		return s.savings.Save(r.Context(), st, calendar.Today())
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
