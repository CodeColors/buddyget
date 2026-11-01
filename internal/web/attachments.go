package web

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"time"

	"github.com/codecolors/buddyget/internal/attachments"
)

// uploadAttachment joint un fichier (photo ou PDF) à une opération. Le
// fichier est lu en flux depuis la requête multipart, sans copie en mémoire
// au-delà de la limite de taille.
func (s *Server) uploadAttachment(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	back := "/operations/" + strconv.FormatInt(id, 10)
	allowLongTransfer(w)
	r.Body = http.MaxBytesReader(w, r.Body, s.maxUpload+maxFormBytes)
	mr, err := r.MultipartReader()
	if err != nil {
		s.renderError(w, r, http.StatusBadRequest, "Formulaire illisible.")
		return
	}
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			s.redirect(w, r, back, "Choisissez un fichier.")
			return
		}
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			s.redirect(w, r, back, "Fichier trop volumineux.")
			return
		}
		if err != nil {
			s.renderError(w, r, http.StatusBadRequest, "Formulaire illisible.")
			return
		}
		if part.FormName() != "file" || part.FileName() == "" {
			continue
		}
		_, err = s.attachments.Add(r.Context(), id, part.FileName(), part)
		if errors.As(err, &tooBig) {
			s.redirect(w, r, back, "Fichier trop volumineux.")
			return
		}
		if err != nil {
			s.failed(w, r, err, back)
			return
		}
		s.redirect(w, r, back, "Pièce jointe ajoutée.")
		return
	}
}

// serveAttachment envoie une pièce jointe : les images s'affichent, les PDF
// se téléchargent (la visionneuse du navigateur ne s'accommode pas de la
// politique de sécurité des pages).
func (s *Server) serveAttachment(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	a, f, err := s.attachments.Open(r.Context(), id)
	if err != nil {
		if errors.Is(err, attachments.ErrNotFound) {
			s.notFound(w, r)
			return
		}
		s.serverError(w, r, err)
		return
	}
	defer func() { _ = f.Close() }()
	disposition := "inline"
	if a.Kind == attachments.PDF {
		disposition = "attachment"
	}
	h := w.Header()
	h.Set("Content-Type", a.ContentType())
	h.Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": a.Name}))
	h.Set("Cache-Control", "private, max-age=86400")
	http.ServeContent(w, r, "", time.Time{}, f)
}

func (s *Server) deleteAttachment(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok || parseForm(w, r) != nil {
		s.notFound(w, r)
		return
	}
	back := localPath(r.PostFormValue("back"))
	if err := s.attachments.Delete(r.Context(), id); err != nil {
		s.failed(w, r, err, back)
		return
	}
	s.redirect(w, r, back, "Pièce jointe supprimée.")
}
