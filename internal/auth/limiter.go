package auth

import (
	"maps"
	"sync"
	"time"
)

// limiter compte les échecs de connexion par clé (adresse IP, identifiant)
// sur une fenêtre fixe. Il vit en mémoire, sans tâche de fond : les entrées
// périmées sont purgées quand la table atteint sa taille maximale.
type limiter struct {
	mu         sync.Mutex
	maxFails   int
	window     time.Duration
	maxEntries int
	entries    map[string]attempts
}

type attempts struct {
	fails int
	start time.Time
}

func newLimiter(maxFails int, window time.Duration, maxEntries int) *limiter {
	return &limiter{maxFails: maxFails, window: window, maxEntries: maxEntries, entries: map[string]attempts{}}
}

// retryAfter renvoie le temps d'attente restant si l'une des clés est
// bloquée, zéro sinon.
func (l *limiter) retryAfter(now time.Time, keys ...string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	var wait time.Duration
	for _, k := range keys {
		a, ok := l.entries[k]
		if !ok || a.fails < l.maxFails {
			continue
		}
		if d := a.start.Add(l.window).Sub(now); d > wait {
			wait = d
		}
	}
	return wait
}

// fail enregistre un échec pour chaque clé.
func (l *limiter) fail(now time.Time, keys ...string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, k := range keys {
		a, ok := l.entries[k]
		if !ok || now.Sub(a.start) >= l.window {
			if !ok && len(l.entries) >= l.maxEntries {
				l.purge(now)
				if len(l.entries) >= l.maxEntries {
					continue // table saturée : la clé n'est pas suivie
				}
			}
			a = attempts{start: now}
		}
		a.fails++
		l.entries[k] = a
	}
}

// reset efface l'historique des clés après une connexion réussie.
func (l *limiter) reset(keys ...string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, k := range keys {
		delete(l.entries, k)
	}
}

func (l *limiter) purge(now time.Time) {
	maps.DeleteFunc(l.entries, func(_ string, a attempts) bool {
		return now.Sub(a.start) >= l.window
	})
}
