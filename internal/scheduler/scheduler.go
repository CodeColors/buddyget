// Package scheduler exécute les travaux quotidiens (inscription des
// intérêts au 31/12, puis alertes et sauvegardes) : un seul minuteur, réarmé
// chaque jour, sans interrogation périodique.
package scheduler

import (
	"context"
	"log/slog"
	"time"

	"github.com/codecolors/buddyget/internal/platform/calendar"
)

// Job est un travail quotidien ; today est la date locale du jour.
type Job struct {
	Name string
	Run  func(ctx context.Context, today calendar.Date) error
}

// Run exécute les travaux au démarrage, puis chaque jour à 00h05 (heure
// locale), jusqu'à l'annulation du contexte. Un travail en échec est
// journalisé sans empêcher les autres ; il sera retenté le lendemain.
func Run(ctx context.Context, jobs ...Job) {
	for {
		for _, j := range jobs {
			if ctx.Err() != nil {
				return
			}
			if err := j.Run(ctx, calendar.Today()); err != nil {
				slog.ErrorContext(ctx, "travail quotidien en échec", "travail", j.Name, "err", err)
			}
		}
		timer := time.NewTimer(time.Until(next(time.Now())))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// next renvoie le prochain passage : 00h05 heure locale, le jour même s'il
// n'est pas encore passé, sinon le lendemain.
func next(now time.Time) time.Time {
	t := time.Date(now.Year(), now.Month(), now.Day(), 0, 5, 0, 0, now.Location())
	if !t.After(now) {
		t = time.Date(now.Year(), now.Month(), now.Day()+1, 0, 5, 0, 0, now.Location())
	}
	return t
}
