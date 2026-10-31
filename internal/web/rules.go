package web

import (
	"net/http"
	"strconv"

	"github.com/codecolors/buddyget/internal/categorize"
	"github.com/codecolors/buddyget/internal/ledger"
)

func (s *Server) rulesPage(w http.ResponseWriter, r *http.Request) {
	rules, err := s.rules.Rules(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "rules", "Règles de catégorisation", rules)
}

// ruleForm est la donnée du formulaire de règle ; il sert aussi à la règle
// « en un clic » proposée depuis une opération.
type ruleForm struct {
	ID         int64
	Pattern    string
	Match      categorize.MatchKind
	CategoryID int64
	Priority   int
	Apply      bool // appliquer aux opérations existantes sans catégorie
	Overwrite  bool // y compris à celles déjà catégorisées
	Back       string
	Error      string
	Kinds      []categorize.MatchKind
	Categories []ledger.KindGroup
}

func (f ruleForm) title() string {
	if f.ID == 0 {
		return "Nouvelle règle"
	}
	return "Modifier la règle"
}

func (s *Server) renderRuleForm(w http.ResponseWriter, r *http.Request, status int, f ruleForm) {
	cats, err := s.ledger.Categories(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	f.Kinds, f.Categories = categorize.MatchKinds, ledger.GroupByKind(cats)
	s.render(w, r, status, "rule_form", f.title(), f)
}

func (s *Server) newRulePage(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	s.renderRuleForm(w, r, http.StatusOK, ruleForm{
		Pattern:    q.Get("motif"),
		Match:      categorize.MatchContains,
		CategoryID: idParam(q.Get("categorie")),
		Apply:      true,
		Back:       "/regles",
	})
}

func (s *Server) editRulePage(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	rule, err := s.rules.Rule(r.Context(), id)
	if err != nil {
		s.inputError(w, r, err)
		return
	}
	s.renderRuleForm(w, r, http.StatusOK, ruleForm{
		ID: id, Pattern: rule.Pattern, Match: rule.Match, CategoryID: rule.CategoryID, Priority: rule.Priority,
		Back: "/regles",
	})
}

func (s *Server) createRule(w http.ResponseWriter, r *http.Request) {
	s.saveRule(w, r, 0)
}

func (s *Server) updateRule(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	s.saveRule(w, r, id)
}

// saveRule enregistre la règle puis, si demandé, l'applique aux opérations
// existantes. Le formulaire peut venir de la page d'une opération : le
// retour s'y fait alors.
func (s *Server) saveRule(w http.ResponseWriter, r *http.Request, id int64) {
	if err := parseForm(w, r); err != nil {
		s.renderError(w, r, http.StatusBadRequest, "Formulaire illisible.")
		return
	}
	priority, _ := strconv.Atoi(r.PostFormValue("priority"))
	f := ruleForm{
		ID:         id,
		Pattern:    r.PostFormValue("pattern"),
		Match:      categorize.MatchKind(r.PostFormValue("match")),
		CategoryID: formID(r, "category"),
		Priority:   priority,
		Apply:      r.PostFormValue("apply") != "",
		Overwrite:  r.PostFormValue("overwrite") != "",
		Back:       localPath(r.PostFormValue("back")),
	}
	in := categorize.RuleInput{Pattern: f.Pattern, Match: f.Match, CategoryID: f.CategoryID, Priority: f.Priority}
	var err error
	if id == 0 {
		id, err = s.rules.CreateRule(r.Context(), in)
	} else {
		err = s.rules.UpdateRule(r.Context(), id, in)
	}
	if err != nil {
		if msg, ok := s.inputError(w, r, err); ok {
			f.Error = msg
			s.renderRuleForm(w, r, http.StatusUnprocessableEntity, f)
		}
		return
	}

	msg := "Règle enregistrée."
	if f.Apply {
		n, err := s.rules.Apply(r.Context(), id, f.Overwrite)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		msg += " " + plural(n, "opération catégorisée", "opérations catégorisées") + "."
	}
	s.redirect(w, r, f.Back, msg)
}

func (s *Server) deleteRule(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	if err := s.rules.DeleteRule(r.Context(), id); err != nil {
		s.failed(w, r, err, "/regles")
		return
	}
	s.redirect(w, r, "/regles", "Règle supprimée.")
}

// applyRules applique toutes les règles aux opérations sans catégorie.
func (s *Server) applyRules(w http.ResponseWriter, r *http.Request) {
	n, err := s.rules.Apply(r.Context(), 0, false)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.redirect(w, r, "/regles", plural(n, "opération catégorisée", "opérations catégorisées")+".")
}
