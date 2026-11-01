package savings

import (
	"math"
	"slices"
	"time"

	"github.com/codecolors/buddyget/internal/platform/calendar"
)

// Move est un mouvement d'un compte d'épargne. Interest marque une écriture
// d'intérêts.
type Move struct {
	Date     calendar.Date
	Amount   int64
	Interest bool
}

// Start est l'état d'un compte au matin du 1er janvier : solde, et part de
// ce solde faite d'intérêts déjà crédités.
type Start struct {
	Balance  int64
	Interest int64
}

// Interest calcule les intérêts acquis du 1er janvier de l'année de to
// jusqu'au jour to inclus (au 31 décembre : ceux de l'année entière).
// Seuls comptent les mouvements de cette année-là, déjà triés ou non.
//
//   - Simple et composé : chaque jour rapporte taux / nombre de jours de
//     l'année sur le solde de la veille (valeur J+1) ; en intérêts simples,
//     la part d'intérêts déjà crédités ne rapporte rien.
//   - Quinzaine : chaque quinzaine (du 1er au 15, du 16 à la fin du mois)
//     rapporte taux / 24 sur le solde qui a valeur à son début : un
//     versement compte à partir de la quinzaine suivante, un retrait dès le
//     début de la quinzaine où il a lieu. Seules les quinzaines achevées
//     au jour to comptent.
func Interest(st Settings, start Start, moves []Move, to calendar.Date) int64 {
	if !st.EarnsInterest() {
		return 0
	}
	t := to.Time()
	first := time.Date(t.Year(), 1, 1, 0, 0, 0, 0, time.UTC)
	inYear := make([]Move, 0, len(moves))
	for _, m := range moves {
		if m.Date.Time().Year() == t.Year() {
			inYear = append(inYear, m)
		}
	}
	var total float64
	if st.Method == Fortnight {
		total = fortnightInterest(st.Rate, start.Balance, inYear, first, t)
	} else {
		total = dailyInterest(st, start, inYear, first, t)
	}
	return int64(math.Round(total))
}

func dailyInterest(st Settings, start Start, moves []Move, first, to time.Time) float64 {
	yearDays := float64(first.AddDate(1, 0, 0).Sub(first).Hours() / 24)
	daily := float64(st.Rate) / 10000 / yearDays
	// Variation du solde (et de sa part d'intérêts) jour par jour.
	type delta struct{ balance, interest int64 }
	changes := map[int]delta{}
	for _, m := range moves {
		day := int(m.Date.Time().Sub(first).Hours() / 24)
		d := changes[day]
		d.balance += m.Amount
		if m.Interest {
			d.interest += m.Amount
		}
		changes[day] = d
	}
	balance, interest := start.Balance, start.Interest
	var total float64
	last := int(to.Sub(first).Hours() / 24)
	for day := 0; day <= last; day++ {
		base := balance
		if st.Method == Simple {
			base -= interest
		}
		total += float64(max(base, 0)) * daily
		d := changes[day]
		balance += d.balance
		interest += d.interest
	}
	return total
}

func fortnightInterest(rate int, start int64, moves []Move, first, to time.Time) float64 {
	// Solde ayant valeur au début de chacune des 24 quinzaines.
	var deltas [25]int64
	for _, m := range moves {
		q := fortnight(m.Date.Time())
		if m.Amount > 0 {
			q++ // un versement compte à partir de la quinzaine suivante
		}
		deltas[q] += m.Amount
	}
	var total float64
	balance := start
	for q := range 24 {
		balance += deltas[q]
		if fortnightEnd(first, q).After(to) {
			break
		}
		total += float64(max(balance, 0)) * float64(rate) / 10000 / 24
	}
	return total
}

// fortnight renvoie le rang (0 à 23) de la quinzaine d'une date.
func fortnight(t time.Time) int {
	q := 2 * (int(t.Month()) - 1)
	if t.Day() > 15 {
		q++
	}
	return q
}

// fortnightEnd renvoie le dernier jour de la quinzaine q de l'année.
func fortnightEnd(first time.Time, q int) time.Time {
	month := first.AddDate(0, q/2, 0)
	if q%2 == 0 {
		return month.AddDate(0, 0, 14)
	}
	return month.AddDate(0, 1, -1)
}

// ProjectInterest renvoie les intérêts à créditer chaque 31 décembre de
// l'intervalle ]from, to], d'après les mouvements du compte, passés et
// prévus. Une année dont les intérêts sont déjà inscrits est sautée ; les
// intérêts projetés d'une année rapportent l'année suivante.
func ProjectInterest(st Settings, moves []Move, from, to calendar.Date) []Move {
	if !st.EarnsInterest() {
		return nil
	}
	all := slices.Clone(moves)
	var out []Move
	for y := from.Time().Year(); yearEnd(y) <= to; y++ {
		if yearEnd(y) <= from {
			continue
		}
		start, in := yearStart(all, y)
		if slices.ContainsFunc(in, func(m Move) bool { return m.Interest }) {
			continue
		}
		if amount := Interest(st, start, in, yearEnd(y)); amount > 0 {
			m := Move{Date: yearEnd(y), Amount: amount, Interest: true}
			out = append(out, m)
			all = append(all, m)
		}
	}
	return out
}
