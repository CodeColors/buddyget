package web

import "net/http"

func (s *Server) tagsPage(w http.ResponseWriter, r *http.Request) {
	tags, err := s.ledger.Tags(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "tags", "Étiquettes", tags)
}

func (s *Server) renameTag(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok || parseForm(w, r) != nil {
		s.notFound(w, r)
		return
	}
	if err := s.ledger.RenameTag(r.Context(), id, r.PostFormValue("name")); err != nil {
		s.failed(w, r, err, "/etiquettes")
		return
	}
	s.redirect(w, r, "/etiquettes", "Étiquette renommée.")
}

func (s *Server) deleteTag(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	if err := s.ledger.DeleteTag(r.Context(), id); err != nil {
		s.failed(w, r, err, "/etiquettes")
		return
	}
	s.redirect(w, r, "/etiquettes", "Étiquette retirée de toutes ses opérations.")
}
