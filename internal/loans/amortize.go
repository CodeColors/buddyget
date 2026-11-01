// Package loans suit les crédits amortissables : tableau d'amortissement,
// remboursements anticipés, et la récurrence de la mensualité, générée et
// tenue à jour par le crédit (pas de double saisie).
package loans

import (
	"math"
	"slices"
	"strings"

	"github.com/codecolors/buddyget/internal/platform/calendar"
)

// maxRows borne un tableau d'amortissement (50 ans).
const maxRows = 600

// Params décrit un crédit à mensualités constantes.
type Params struct {
	Principal int64         // capital emprunté
	Rate      int           // taux nominal annuel, millièmes de point (3 450 = 3,450 %)
	Months    int           // durée
	Insurance int64         // assurance mensuelle, hors mensualité
	First     calendar.Date // première échéance
}

// Prepayment est un remboursement anticipé : la mensualité reste la même,
// la durée raccourcit.
type Prepayment struct {
	Date   calendar.Date
	Amount int64
}

// Row est une ligne du tableau d'amortissement.
type Row struct {
	N         int // rang de l'échéance, à partir de 1
	Date      calendar.Date
	Prepaid   int64 // remboursement anticipé versé avant l'échéance
	Payment   int64 // mensualité hors assurance : intérêts + capital
	Interest  int64
	Capital   int64
	Insurance int64
	Remaining int64 // capital restant dû après l'échéance
}

// Schedule est le tableau d'amortissement d'un crédit.
type Schedule struct {
	Rows           []Row
	Payment        int64 // mensualité courante, hors assurance
	TotalInterest  int64
	TotalInsurance int64
	TotalPrepaid   int64
	End            calendar.Date // dernière échéance, ou jour du remboursement qui solde le crédit
}

// monthlyRate renvoie le taux mensuel d'un taux annuel en millièmes de point.
func monthlyRate(rate int) float64 { return float64(rate) / 100_000 / 12 }

// Payment renvoie la mensualité hors assurance, arrondie au centime.
func Payment(p Params) int64 {
	if p.Months <= 0 {
		return 0
	}
	r := monthlyRate(p.Rate)
	if r == 0 {
		return int64(math.Ceil(float64(p.Principal) / float64(p.Months)))
	}
	return int64(math.Round(float64(p.Principal) * r / (1 - math.Pow(1+r, -float64(p.Months)))))
}

// Amortize établit le tableau d'amortissement. Chaque remboursement
// anticipé réduit le capital avant l'échéance qui le suit ; la mensualité
// est conservée, la dernière échéance solde le capital restant.
func Amortize(p Params, prepayments []Prepayment) Schedule {
	s := Schedule{Payment: Payment(p)}
	prepayments = slices.Clone(prepayments)
	slices.SortFunc(prepayments, func(a, b Prepayment) int { return strings.Compare(string(a.Date), string(b.Date)) })
	r := monthlyRate(p.Rate)
	balance := p.Principal
	for k := 0; balance > 0 && k < maxRows; k++ {
		row := Row{N: k + 1, Date: calendar.Monthly.Nth(p.First, k, false), Insurance: p.Insurance}
		for len(prepayments) > 0 && prepayments[0].Date <= row.Date && balance > 0 {
			paid := min(prepayments[0].Amount, balance)
			row.Prepaid += paid
			balance -= paid
			if balance == 0 {
				// Soldé par anticipation, le jour du remboursement : plus
				// d'échéance.
				row.Date, row.Insurance = prepayments[0].Date, 0
			}
			prepayments = prepayments[1:]
		}
		if balance == 0 {
			s.TotalPrepaid += row.Prepaid
			s.Rows = append(s.Rows, row)
			break
		}
		row.Interest = int64(math.Round(float64(balance) * r))
		row.Capital = s.Payment - row.Interest
		if row.Capital >= balance || k == p.Months-1 {
			row.Capital = balance
		}
		row.Payment = row.Interest + row.Capital
		balance -= row.Capital
		row.Remaining = balance
		s.TotalInterest += row.Interest
		s.TotalInsurance += row.Insurance
		s.TotalPrepaid += row.Prepaid
		s.Rows = append(s.Rows, row)
	}
	if n := len(s.Rows); n > 0 {
		s.End = s.Rows[n-1].Date
	}
	return s
}

// Status résume un crédit à une date.
type Status struct {
	Remaining    int64 // capital restant dû après les échéances passées
	InterestPaid int64 // intérêts payés jusqu'à cette date
	Paid         int   // échéances payées
	Next         Row   // prochaine échéance (vide si le crédit est soldé)
	Done         bool
}

// StatusAt résume un tableau d'amortissement au jour on (échéances du jour
// comprises).
func (s Schedule) StatusAt(principal int64, on calendar.Date) Status {
	st := Status{Remaining: principal}
	for _, row := range s.Rows {
		if row.Date > on {
			st.Next = row
			return st
		}
		st.Remaining = row.Remaining
		st.InterestPaid += row.Interest
		st.Paid++
	}
	st.Done = true
	return st
}
