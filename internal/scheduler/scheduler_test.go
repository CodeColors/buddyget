package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/codecolors/buddyget/internal/platform/calendar"
)

func TestNext(t *testing.T) {
	paris, err := time.LoadLocation("Europe/Paris")
	if err != nil {
		t.Fatal(err)
	}
	for now, want := range map[time.Time]time.Time{
		time.Date(2026, 12, 31, 23, 59, 0, 0, paris): time.Date(2027, 1, 1, 0, 5, 0, 0, paris),
		time.Date(2026, 10, 31, 0, 2, 0, 0, paris):   time.Date(2026, 10, 31, 0, 5, 0, 0, paris),
		time.Date(2026, 10, 31, 0, 5, 0, 0, paris):   time.Date(2026, 11, 1, 0, 5, 0, 0, paris),
		time.Date(2026, 10, 24, 12, 0, 0, 0, paris):  time.Date(2026, 10, 25, 0, 5, 0, 0, paris), // passage à l'heure d'hiver
	} {
		if got := next(now); !got.Equal(want) {
			t.Errorf("après %s : %s, attendu %s", now, got, want)
		}
	}
}

func TestRunStopsWithContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	var ran []string
	done := make(chan struct{})
	go func() {
		Run(ctx,
			Job{Name: "en échec", Run: func(context.Context, calendar.Date) error { ran = append(ran, "a"); return errors.New("panne") }},
			Job{Name: "suivant", Run: func(_ context.Context, today calendar.Date) error {
				ran = append(ran, string(today))
				cancel()
				return nil
			}},
			Job{Name: "après l'arrêt", Run: func(context.Context, calendar.Date) error { ran = append(ran, "c"); return nil }},
		)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run ne s'arrête pas avec le contexte")
	}
	if len(ran) != 2 || ran[0] != "a" || ran[1] != string(calendar.Today()) {
		t.Errorf("travaux exécutés : %v", ran)
	}
}
