package web

import (
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/codecolors/buddyget/internal/exports"
	"github.com/codecolors/buddyget/internal/ledger"
	"github.com/codecolors/buddyget/internal/platform/calendar"
)

// exportsView est la donnée de la page des exports.
type exportsView struct {
	Accounts   []ledger.AccountBalance
	Categories []ledger.KindGroup
	Tags       []ledger.Tag
	Today      calendar.Date
	YearStart  calendar.Date
	Month      string // mois courant, AAAA-MM
	Year       int
}

func (s *Server) exportsPage(w http.ResponseWriter, r *http.Request) {
	ctx, today := r.Context(), calendar.Today()
	v := exportsView{Today: today, Month: string(today)[:7], Year: today.Time().Year()}
	v.YearStart = calendar.FromTime(time.Date(v.Year, 1, 1, 0, 0, 0, 0, time.UTC))
	accounts, err := s.ledger.Accounts(ctx, today)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	v.Accounts = accounts
	cats, err := s.ledger.Categories(ctx)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	v.Categories = ledger.GroupByKind(cats)
	if v.Tags, err = s.ledger.Tags(ctx); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "exports", "Exports", v)
}

// download prépare une réponse à enregistrer sous name, puis l'écrit avec
// write. Une erreur survenue en cours d'écriture ne peut plus changer la
// réponse : elle est journalisée, le fichier reçu est tronqué.
func download(w http.ResponseWriter, r *http.Request, name, contentType string, write func(io.Writer) error) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	w.Header().Set("Cache-Control", "no-store")
	if err := write(w); err != nil {
		slog.ErrorContext(r.Context(), "export interrompu", "fichier", name, "err", err)
	}
}

// exportName nomme un fichier exporté : buddyget-<sujet>-<date>.<ext>.
func exportName(subject, ext string) string {
	return "buddyget-" + subject + "-" + string(calendar.Today()) + "." + ext
}

// exportOperations exporte en CSV les opérations des critères de la liste
// (mêmes paramètres que /operations).
func (s *Server) exportOperations(w http.ResponseWriter, r *http.Request) {
	f, _ := parseFilter(r.URL.Query())
	download(w, r, exportName("operations", "csv"), "text/csv; charset=utf-8", func(out io.Writer) error {
		return s.exports.Operations(r.Context(), out, f)
	})
}

// exportReport exporte un rapport (?mois= ou ?annee=) en PDF ou en CSV
// (?format=csv).
func (s *Server) exportReport(w http.ResponseWriter, r *http.Request) {
	v, err := s.report(r)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	title := "Rapport " + v.Period
	subject := "rapport-" + strings.TrimPrefix(strings.TrimPrefix(v.Query, "mois="), "annee=")
	if r.FormValue("format") == "csv" {
		download(w, r, exportName(subject, "csv"), "text/csv; charset=utf-8", func(out io.Writer) error {
			return exports.ReportCSV(out, title, v.Report)
		})
		return
	}
	download(w, r, exportName(subject, "pdf"), "application/pdf", func(out io.Writer) error {
		return exports.ReportPDF(out, title, v.Report, time.Now())
	})
}

// exportDossier exporte une archive ZIP : opérations des critères, synthèse
// PDF et, avec ?pieces=1, les pièces jointes.
func (s *Server) exportDossier(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f, _ := parseFilter(q)
	scope, err := s.describeFilter(r, f)
	if err != nil {
		s.inputError(w, r, err)
		return
	}
	download(w, r, exportName("dossier", "zip"), "application/zip", func(out io.Writer) error {
		return s.exports.Dossier(r.Context(), out, f, scope, q.Get("pieces") == "1", time.Now())
	})
}

// describeFilter décrit en clair les critères d'un dossier.
func (s *Server) describeFilter(r *http.Request, f ledger.Filter) (string, error) {
	var parts []string
	switch {
	case f.From != "" && f.To != "":
		parts = append(parts, "du "+f.From.Format()+" au "+f.To.Format())
	case f.From != "":
		parts = append(parts, "depuis le "+f.From.Format())
	case f.To != "":
		parts = append(parts, "jusqu'au "+f.To.Format())
	default:
		parts = append(parts, "toutes les dates")
	}
	if f.AccountID != 0 {
		a, err := s.ledger.Account(r.Context(), f.AccountID)
		if err != nil {
			return "", err
		}
		parts = append(parts, "compte "+a.Name)
	}
	if f.CategoryID > 0 {
		cats, err := s.ledger.Categories(r.Context())
		if err != nil {
			return "", err
		}
		for _, c := range cats {
			if c.ID == f.CategoryID {
				parts = append(parts, "catégorie "+c.FullName())
			}
		}
	}
	if f.Tag != "" {
		parts = append(parts, "étiquette « "+f.Tag+" »")
	}
	return "Opérations " + strings.Join(parts, ", ") + ".", nil
}
