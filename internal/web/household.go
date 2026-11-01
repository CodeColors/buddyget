package web

import (
	"net/http"
	"strconv"

	"github.com/codecolors/buddyget/internal/household"
	"github.com/codecolors/buddyget/internal/ledger"
	"github.com/codecolors/buddyget/internal/platform/calendar"
	"github.com/codecolors/buddyget/internal/platform/money"
)

// sharedLimit est le nombre d'opérations partagées récentes affichées.
const sharedLimit = 20

// householdView est la donnée de la page du partage.
type householdView struct {
	Members     []household.Member
	Balances    []household.Balance
	Debts       []household.Debt
	Shared      []household.Shared
	Settlements []household.Settlement
	Accounts    []ledger.AccountBalance
	Member      memberForm
	Settle      settleForm
}

// memberForm est la saisie d'un membre.
type memberForm struct {
	ID       int64
	Name     string
	Income   string
	Archived bool
	Error    string
}

// settleForm est la saisie d'un remboursement.
type settleForm struct {
	Date          string
	FromID, ToID  int64
	Amount        string
	FromAccountID int64
	ToAccountID   int64
	Error         string
}

func (s *Server) householdPage(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	s.renderHousehold(w, r, http.StatusOK, memberForm{}, settleForm{
		Date: string(calendar.Today()), FromID: idParam(q.Get("de")), ToID: idParam(q.Get("a")), Amount: q.Get("montant"),
	})
}

func (s *Server) renderHousehold(w http.ResponseWriter, r *http.Request, status int, mf memberForm, sf settleForm) {
	ctx := r.Context()
	v := householdView{Member: mf, Settle: sf}
	var err error
	if v.Members, err = s.household.Members(ctx); err != nil {
		s.serverError(w, r, err)
		return
	}
	if v.Balances, v.Debts, err = s.household.Balances(ctx); err != nil {
		s.serverError(w, r, err)
		return
	}
	if v.Shared, err = s.household.SharedTransactions(ctx, sharedLimit); err != nil {
		s.serverError(w, r, err)
		return
	}
	if v.Settlements, err = s.household.Settlements(ctx); err != nil {
		s.serverError(w, r, err)
		return
	}
	accounts, err := s.ledger.Accounts(ctx, calendar.Today())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	for _, a := range accounts {
		if !a.Archived {
			v.Accounts = append(v.Accounts, a)
		}
	}
	s.render(w, r, status, "household", "Partage du foyer", v)
}

func (s *Server) createMember(w http.ResponseWriter, r *http.Request) {
	if parseForm(w, r) != nil {
		s.renderError(w, r, http.StatusBadRequest, "Formulaire illisible.")
		return
	}
	f := memberForm{Name: r.PostFormValue("name"), Income: r.PostFormValue("income")}
	in, err := f.input()
	if err == nil {
		_, err = s.household.CreateMember(r.Context(), in)
	}
	if err != nil {
		if msg, ok := s.inputError(w, r, err); ok {
			f.Error = msg
			s.renderHousehold(w, r, http.StatusUnprocessableEntity, f, settleForm{Date: string(calendar.Today())})
		}
		return
	}
	s.redirect(w, r, "/partage", "Membre ajouté.")
}

// input lit la saisie d'un membre ; revenu vide : 0.
func (f memberForm) input() (household.MemberInput, error) {
	in := household.MemberInput{Name: f.Name}
	if f.Income == "" {
		return in, nil
	}
	income, err := money.Parse(f.Income)
	in.Income = max(income, -income)
	return in, err
}

func (s *Server) editMemberPage(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	m, err := s.household.Member(r.Context(), id)
	if err != nil {
		s.inputError(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "member_form", "Modifier un membre",
		memberForm{ID: id, Name: m.Name, Income: money.FormatInput(m.Income), Archived: m.Archived})
}

func (s *Server) updateMember(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	if parseForm(w, r) != nil {
		s.renderError(w, r, http.StatusBadRequest, "Formulaire illisible.")
		return
	}
	f := memberForm{ID: id, Name: r.PostFormValue("name"), Income: r.PostFormValue("income")}
	in, err := f.input()
	if err == nil {
		err = s.household.UpdateMember(r.Context(), id, in)
	}
	if err != nil {
		if msg, ok := s.inputError(w, r, err); ok {
			f.Error = msg
			s.render(w, r, http.StatusUnprocessableEntity, "member_form", "Modifier un membre", f)
		}
		return
	}
	s.redirect(w, r, "/partage", "Membre enregistré.")
}

func (s *Server) archiveMember(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	if parseForm(w, r) != nil {
		s.renderError(w, r, http.StatusBadRequest, "Formulaire illisible.")
		return
	}
	archived := r.PostFormValue("archived") == "1"
	if err := s.household.SetArchived(r.Context(), id, archived); err != nil {
		s.failed(w, r, err, "/partage")
		return
	}
	msg := "Membre réactivé."
	if archived {
		msg = "Membre archivé : il n'est plus proposé dans les partages."
	}
	s.redirect(w, r, "/partage", msg)
}

func (s *Server) deleteMember(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	if err := s.household.DeleteMember(r.Context(), id); err != nil {
		s.failed(w, r, err, "/partage/membres/"+strconv.FormatInt(id, 10))
		return
	}
	s.redirect(w, r, "/partage", "Membre supprimé.")
}

func (s *Server) settle(w http.ResponseWriter, r *http.Request) {
	if parseForm(w, r) != nil {
		s.renderError(w, r, http.StatusBadRequest, "Formulaire illisible.")
		return
	}
	f := settleForm{
		Date: r.PostFormValue("date"), FromID: formID(r, "from"), ToID: formID(r, "to"), Amount: r.PostFormValue("amount"),
		FromAccountID: formID(r, "from_account"), ToAccountID: formID(r, "to_account"),
	}
	err := func() error {
		date, err := calendar.Parse(f.Date)
		if err != nil {
			return err
		}
		amount, err := money.Parse(f.Amount)
		if err != nil {
			return err
		}
		_, err = s.household.Settle(r.Context(), household.SettlementInput{
			Date: date, FromID: f.FromID, ToID: f.ToID, Amount: max(amount, -amount), FromAccountID: f.FromAccountID, ToAccountID: f.ToAccountID,
		})
		return err
	}()
	if err != nil {
		if msg, ok := s.inputError(w, r, err); ok {
			f.Error = msg
			s.renderHousehold(w, r, http.StatusUnprocessableEntity, memberForm{}, f)
		}
		return
	}
	s.redirect(w, r, "/partage", "Remboursement enregistré.")
}

func (s *Server) deleteSettlement(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	if err := s.household.DeleteSettlement(r.Context(), id); err != nil {
		s.failed(w, r, err, "/partage")
		return
	}
	s.redirect(w, r, "/partage", "Remboursement annulé.")
}
