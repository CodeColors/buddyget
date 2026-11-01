package web

import (
	"net/http"

	"github.com/codecolors/buddyget/internal/platform/calendar"
	"github.com/codecolors/buddyget/internal/recurring"
)

// subscriptionsView est la donnée de la vue Abonnements.
type subscriptionsView struct {
	Items   []recurring.Subscription
	Monthly int64
}

// Annual renvoie le coût annuel de l'ensemble.
func (v subscriptionsView) Annual() int64 { return v.Monthly * 12 }

func (s *Server) subscriptionsPage(w http.ResponseWriter, r *http.Request) {
	subs, err := s.recurring.Subscriptions(r.Context(), calendar.Today())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	v := subscriptionsView{Items: subs}
	for _, sub := range subs {
		v.Monthly += sub.Monthly
	}
	s.render(w, r, http.StatusOK, "subscriptions", "Abonnements", v)
}
