package savings

import "github.com/codecolors/buddyget/internal/platform/calendar"

// Availability répartit le solde d'un compte entre fonds disponibles et
// bloqués à une date.
type Availability struct {
	Balance   int64
	Available int64
	Blocked   int64
	// NextUnlock est la prochaine date où des fonds bloqués se libèrent ;
	// vide si rien n'est bloqué.
	NextUnlock calendar.Date
}

// AvailabilityOn calcule les fonds disponibles et bloqués au soir du jour
// on, d'après tous les mouvements du compte (solde initial compris).
//
//   - Date de déblocage : tout le solde est bloqué jusqu'à cette date.
//   - Durée : chaque versement est bloqué LockMonths mois ; les retraits
//     puisent d'abord dans les fonds libres.
func AvailabilityOn(st Settings, moves []Move, on calendar.Date) Availability {
	var a Availability
	var locked int64
	for _, m := range moves {
		if m.Date > on {
			continue
		}
		a.Balance += m.Amount
		if st.LockMonths > 0 && m.Amount > 0 {
			if unlock := calendar.FromTime(m.Date.Time().AddDate(0, st.LockMonths, 0)); unlock > on {
				locked += m.Amount
				if a.NextUnlock == "" || unlock < a.NextUnlock {
					a.NextUnlock = unlock
				}
			}
		}
	}
	if st.UnlockDate != "" && on < st.UnlockDate {
		locked, a.NextUnlock = a.Balance, st.UnlockDate
	}
	a.Blocked = min(max(locked, 0), max(a.Balance, 0))
	if a.Blocked == 0 {
		a.NextUnlock = ""
	}
	a.Available = a.Balance - a.Blocked
	return a
}
