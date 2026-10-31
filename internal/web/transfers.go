package web

import (
	"net/http"

	"github.com/codecolors/buddyget/internal/ledger"
)

// transferCandidatesShown borne la liste des virements à rapprocher.
const transferCandidatesShown = 100

// transfersView est la donnée de la page des virements à rapprocher.
type transfersView struct {
	Candidates []ledger.TransferCandidate
	WindowDays int
}

func (s *Server) transfersPage(w http.ResponseWriter, r *http.Request) {
	candidates, err := s.ledger.TransferCandidates(r.Context(), transferCandidatesShown)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "transfers", "Virements à rapprocher",
		transfersView{Candidates: candidates, WindowDays: ledger.TransferWindowDays})
}

// candidatePair lit la paire (débit, crédit) d'un formulaire.
func candidatePair(w http.ResponseWriter, r *http.Request) (debit, credit int64, ok bool) {
	if parseForm(w, r) != nil {
		return 0, 0, false
	}
	debit, credit = formID(r, "debit"), formID(r, "credit")
	return debit, credit, debit != 0 && credit != 0
}

func (s *Server) linkTransfer(w http.ResponseWriter, r *http.Request) {
	debit, credit, ok := candidatePair(w, r)
	if !ok {
		s.renderError(w, r, http.StatusBadRequest, "Formulaire illisible.")
		return
	}
	if err := s.ledger.LinkTransfer(r.Context(), debit, credit); err != nil {
		s.failed(w, r, err, "/operations/virements")
		return
	}
	s.redirect(w, r, "/operations/virements", "Virement relié : il ne compte plus ni en dépense ni en revenu.")
}

func (s *Server) dismissTransfer(w http.ResponseWriter, r *http.Request) {
	debit, credit, ok := candidatePair(w, r)
	if !ok {
		s.renderError(w, r, http.StatusBadRequest, "Formulaire illisible.")
		return
	}
	if err := s.ledger.DismissTransferCandidate(r.Context(), debit, credit); err != nil {
		s.failed(w, r, err, "/operations/virements")
		return
	}
	s.redirect(w, r, "/operations/virements", "Paire écartée : elle ne sera plus proposée.")
}

// unlinkTransfer défait le virement dont l'opération fait partie.
func (s *Server) unlinkTransfer(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok || parseForm(w, r) != nil {
		s.notFound(w, r)
		return
	}
	t, err := s.ledger.Transaction(r.Context(), id)
	if err == nil && !t.IsTransfer() {
		err = ledger.ErrNotFound
	}
	if err == nil {
		err = s.ledger.UnlinkTransfer(r.Context(), t.TransferID)
	}
	if err != nil {
		s.failed(w, r, err, "/operations")
		return
	}
	s.redirect(w, r, localPath(r.PostFormValue("back")), "Virement défait : les deux opérations sont redevenues simples.")
}

// transferCount renvoie le nombre de virements à rapprocher (borné), pour
// le signaler dans la liste des opérations et après un import.
func (s *Server) transferCount(r *http.Request) (int, error) {
	return s.ledger.CountTransferCandidates(r.Context(), transferCandidatesShown)
}
