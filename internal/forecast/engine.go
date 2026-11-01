// Package forecast calcule le solde prévisionnel à une date choisie : solde
// actuel, échéances des récurrences actives, opérations planifiées et
// dépenses variables estimées. Le moteur (Compute) est une fonction pure et
// déterministe ; le service rassemble ses données.
package forecast

import (
	"cmp"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/codecolors/buddyget/internal/platform/calendar"
)

// Kind est la nature d'un mouvement attendu.
type Kind uint8

const (
	Recurring Kind = iota + 1 // échéance d'une récurrence active
	Planned                   // opération saisie à une date future
	Pending                   // opérations passées non pointées (solde pointé)
	Interest                  // intérêts d'épargne estimés, crédités au 31/12
	Provision                 // lissage : provision mensuelle, utilisée à l'échéance
)

// Label décrit la nature d'un mouvement.
func (k Kind) Label() string {
	switch k {
	case Recurring:
		return "Récurrence"
	case Planned:
		return "Planifiée"
	case Interest:
		return "Intérêts estimés"
	case Provision:
		return "Lissage"
	}
	return "Non pointées"
}

// Event est un mouvement attendu après aujourd'hui.
type Event struct {
	Date        calendar.Date // date effective, reportée au jour ouvré
	Label       string
	Amount      int64
	Kind        Kind
	AccountID   int64
	AccountName string
	Estimated   bool // montant estimé (récurrence à montant variable)
	Transfer    bool // virement avec un compte du foyer hors du prévisionnel
	Scenario    bool // ajouté ou modifié par le scénario appliqué
	// Échéance d'une récurrence : la règle et la date théorique, qui
	// identifient l'échéance pour l'écarter ponctuellement.
	RuleID   int64
	Nominal  calendar.Date
	Excluded bool // écartée : affichée mais non comptée
}

// Input rassemble les données d'un calcul.
type Input struct {
	Today, Target calendar.Date
	Opening       int64 // solde au soir d'aujourd'hui
	Events        []Event
	// Variable est l'estimation mensuelle des dépenses variables (négative),
	// répartie jour par jour au prorata de la longueur de chaque mois.
	Variable  int64
	Threshold int64
}

// Point est le solde au soir d'un jour.
type Point struct {
	Date    calendar.Date
	Balance int64
}

// Line est un mouvement suivi du solde cumulé, dépenses variables du jour
// comprises.
type Line struct {
	Event
	Balance int64
}

// Range est une suite de jours où le solde reste sous le seuil.
type Range struct {
	From, To calendar.Date
	Low      int64
}

// Totals ventile l'écart entre solde actuel et solde prévu : Opening +
// Income + Recurring + Other + Provision + Variable = Closing.
type Totals struct {
	Income    int64 // revenus attendus
	Recurring int64 // dépenses récurrentes
	Other     int64 // dépenses planifiées et non pointées
	Provision int64 // provisions des dépenses lissées, nettes de leur utilisation
	Variable  int64 // dépenses variables estimées
}

// Result est un prévisionnel calculé.
type Result struct {
	Today, Target    calendar.Date
	Opening, Closing int64
	Threshold        int64
	Days             []Point // d'aujourd'hui à la date cible incluse
	Lines            []Line
	Low              Point // premier jour du solde le plus bas
	Below            []Range
	Totals           Totals
}

// Compute calcule le solde jour par jour d'aujourd'hui (exclu) à la date
// cible (incluse). Les mouvements hors de cet intervalle sont ignorés ; un
// même jour, les revenus passent avant les dépenses.
func Compute(in Input) Result {
	events := slices.Clone(in.Events)
	slices.SortStableFunc(events, func(a, b Event) int {
		return cmp.Or(strings.Compare(string(a.Date), string(b.Date)), cmp.Compare(b.Amount, a.Amount),
			strings.Compare(a.Label, b.Label), strings.Compare(a.AccountName, b.AccountName))
	})
	res := Result{Today: in.Today, Target: in.Target, Opening: in.Opening, Threshold: in.Threshold}
	n := max(in.Today.DaysUntil(in.Target), 0)
	res.Days = make([]Point, 1, n+1)
	res.Days[0] = Point{in.Today, in.Opening}

	balance := in.Opening
	var accrued float64 // dépenses variables cumulées, exactes
	i, _ := slices.BinarySearchFunc(events, in.Today.AddDays(1), func(e Event, d calendar.Date) int {
		return strings.Compare(string(e.Date), string(d))
	})
	t := in.Today.Time()
	for range n {
		t = t.AddDate(0, 0, 1)
		day := calendar.FromTime(t)
		if in.Variable != 0 {
			accrued += float64(in.Variable) / float64(daysInMonth(t))
			v := int64(math.Round(accrued))
			balance += v - res.Totals.Variable
			res.Totals.Variable = v
		}
		for ; i < len(events) && events[i].Date == day; i++ {
			e := events[i]
			if !e.Excluded {
				balance += e.Amount
				res.Totals.add(e)
			}
			res.Lines = append(res.Lines, Line{Event: e, Balance: balance})
		}
		res.Days = append(res.Days, Point{day, balance})
	}
	res.Closing = balance
	res.Low, res.Below = lows(res.Days, in.Threshold)
	return res
}

func (t *Totals) add(e Event) {
	switch {
	case e.Kind == Provision:
		t.Provision += e.Amount
	case e.Amount > 0:
		t.Income += e.Amount
	case e.Kind == Recurring:
		t.Recurring += e.Amount
	default:
		t.Other += e.Amount
	}
}

// lows renvoie le point bas et les périodes sous le seuil.
func lows(days []Point, threshold int64) (Point, []Range) {
	low := days[0]
	var below []Range
	open := false
	for _, p := range days {
		if p.Balance < low.Balance {
			low = p
		}
		switch {
		case p.Balance >= threshold:
			open = false
		case open:
			r := &below[len(below)-1]
			r.To, r.Low = p.Date, min(r.Low, p.Balance)
		default:
			below = append(below, Range{From: p.Date, To: p.Date, Low: p.Balance})
			open = true
		}
	}
	return low, below
}

func daysInMonth(t time.Time) int {
	return time.Date(t.Year(), t.Month()+1, 0, 0, 0, 0, 0, time.UTC).Day()
}
