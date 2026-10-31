package web

import (
	"net/http"
	"strconv"

	"github.com/codecolors/buddyget/internal/ledger"
)

func (s *Server) categoriesPage(w http.ResponseWriter, r *http.Request) {
	cats, err := s.ledger.Categories(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "categories", "Catégories", ledger.GroupByKind(cats))
}

// categoryForm est la donnée du formulaire de catégorie.
type categoryForm struct {
	ID       int64
	ParentID int64
	Name     string
	Kind     ledger.CategoryKind
	Color    string
	Error    string
	Kinds    []ledger.CategoryKind
	Parents  []ledger.KindGroup // catégories de premier niveau, parents possibles
}

func (f categoryForm) title() string {
	if f.ID == 0 {
		return "Nouvelle catégorie"
	}
	return "Modifier la catégorie"
}

// categoryFormFor prépare le formulaire avec la liste des parents possibles
// (catégories de premier niveau, sauf celle qu'on modifie).
func (s *Server) categoryFormFor(r *http.Request, id int64) (categoryForm, error) {
	cats, err := s.ledger.Categories(r.Context())
	var parents []ledger.Category
	for _, c := range cats {
		if c.ParentID == 0 && c.ID != id {
			parents = append(parents, c)
		}
	}
	return categoryForm{ID: id, Kinds: ledger.CategoryKinds, Parents: ledger.GroupByKind(parents)}, err
}

func (s *Server) newCategoryPage(w http.ResponseWriter, r *http.Request) {
	f, err := s.categoryFormFor(r, 0)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	f.ParentID, f.Kind, f.Color = formID(r, "parent"), ledger.CategoryExpense, ledger.DefaultColor
	s.render(w, r, http.StatusOK, "category_form", f.title(), f)
}

func (s *Server) editCategoryPage(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	c, err := s.ledger.Category(r.Context(), id)
	if err != nil {
		s.inputError(w, r, err)
		return
	}
	f, err := s.categoryFormFor(r, id)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	f.ParentID, f.Name, f.Kind, f.Color = c.ParentID, c.Name, c.Kind, c.Color
	s.render(w, r, http.StatusOK, "category_form", f.title(), f)
}

func (s *Server) createCategory(w http.ResponseWriter, r *http.Request) {
	s.saveCategory(w, r, 0)
}

func (s *Server) updateCategory(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	s.saveCategory(w, r, id)
}

func (s *Server) saveCategory(w http.ResponseWriter, r *http.Request, id int64) {
	if err := parseForm(w, r); err != nil {
		s.renderError(w, r, http.StatusBadRequest, "Formulaire illisible.")
		return
	}
	in := ledger.CategoryInput{
		ParentID: formID(r, "parent"),
		Name:     r.PostFormValue("name"),
		Kind:     ledger.CategoryKind(r.PostFormValue("kind")),
		Color:    r.PostFormValue("color"),
	}
	var err error
	if id == 0 {
		_, err = s.ledger.CreateCategory(r.Context(), in)
	} else {
		err = s.ledger.UpdateCategory(r.Context(), id, in)
	}
	if err != nil {
		msg, ok := s.inputError(w, r, err)
		if !ok {
			return
		}
		f, err := s.categoryFormFor(r, id)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		f.ParentID, f.Name, f.Kind, f.Color, f.Error = in.ParentID, in.Name, in.Kind, in.Color, msg
		s.render(w, r, http.StatusUnprocessableEntity, "category_form", f.title(), f)
		return
	}
	s.redirect(w, r, "/categories", "Catégorie enregistrée.")
}

func (s *Server) deleteCategory(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	if err := s.ledger.DeleteCategory(r.Context(), id); err != nil {
		s.failed(w, r, err, "/categories/"+strconv.FormatInt(id, 10))
		return
	}
	s.redirect(w, r, "/categories", "Catégorie supprimée.")
}
