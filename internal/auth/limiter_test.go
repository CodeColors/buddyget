package auth

import (
	"testing"
	"time"
)

func TestLimiterBlocksAfterMaxFails(t *testing.T) {
	l := newLimiter(3, time.Minute, 100)
	t0 := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)

	for i := range 3 {
		if d := l.retryAfter(t0, "ip:a"); d != 0 {
			t.Fatalf("bloqué après %d échecs seulement", i)
		}
		l.fail(t0, "ip:a")
	}
	if d := l.retryAfter(t0.Add(10*time.Second), "ip:b", "ip:a"); d != 50*time.Second {
		t.Errorf("attente = %v, attendu 50s", d)
	}
	if d := l.retryAfter(t0, "ip:b"); d != 0 {
		t.Error("une autre clé ne doit pas être bloquée")
	}
	if d := l.retryAfter(t0.Add(time.Minute), "ip:a"); d != 0 {
		t.Error("le blocage doit cesser à la fin de la fenêtre")
	}

	// Un échec après la fenêtre ouvre une nouvelle fenêtre.
	l.fail(t0.Add(time.Minute), "ip:a")
	if d := l.retryAfter(t0.Add(time.Minute), "ip:a"); d != 0 {
		t.Error("le compteur aurait dû repartir de zéro")
	}
}

func TestLimiterReset(t *testing.T) {
	l := newLimiter(1, time.Minute, 100)
	now := time.Now()
	l.fail(now, "ip:a", "user:x")
	l.reset("ip:a", "user:x")
	if d := l.retryAfter(now, "ip:a", "user:x"); d != 0 {
		t.Error("reset doit lever le blocage")
	}
}

func TestLimiterBoundedSize(t *testing.T) {
	l := newLimiter(1, time.Minute, 2)
	t0 := time.Now()
	l.fail(t0, "a", "b")
	l.fail(t0, "c") // table pleine, rien d'expiré : c n'est pas suivie
	if len(l.entries) != 2 || l.retryAfter(t0, "c") != 0 {
		t.Fatalf("la table ne doit pas dépasser 2 entrées : %v", l.entries)
	}
	l.fail(t0.Add(time.Minute), "c") // a et b ont expiré : purge
	if len(l.entries) != 1 || l.retryAfter(t0.Add(time.Minute), "c") == 0 {
		t.Errorf("après purge, seule c doit rester et être bloquée : %v", l.entries)
	}
}
