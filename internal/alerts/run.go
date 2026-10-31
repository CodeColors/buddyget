package alerts

import (
	"context"
	"log/slog"

	"github.com/codecolors/buddyget/internal/platform/calendar"
)

// Trigger demande un examen de la situation (après une saisie ou un import)
// sans attendre : les demandes rapprochées n'en font qu'une.
func (s *Service) Trigger() {
	select {
	case s.trigger <- struct{}{}:
	default:
	}
}

// Run traite les demandes d'examen jusqu'à l'annulation du contexte.
func (s *Service) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.trigger:
			if err := s.refresh(ctx, calendar.Today(), false); err != nil {
				slog.ErrorContext(ctx, "examen des alertes", "err", err)
			}
		}
	}
}

// Daily est le travail quotidien : examen, puis récapitulatif éventuel.
func (s *Service) Daily(ctx context.Context, today calendar.Date) error {
	return s.refresh(ctx, today, true)
}

// refresh examine la situation puis relaie les alertes nouvelles ; un seul
// examen à la fois, pour ne jamais envoyer deux fois la même alerte.
func (s *Service) refresh(ctx context.Context, today calendar.Date, daily bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.Evaluate(ctx, today); err != nil {
		return err
	}
	return s.Flush(ctx, today, daily)
}
