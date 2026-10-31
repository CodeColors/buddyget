package recurring

import (
	"cmp"
	"fmt"
	"math"
	"slices"
	"strings"
	"unicode"

	"github.com/codecolors/buddyget/internal/platform/calendar"
	"github.com/codecolors/buddyget/internal/platform/text"
)

// Paramètres de la détection (cahier des charges, section 6.1).
const (
	dateTolerance   = 3    // jours d'écart tolérés autour de l'échéance attendue
	amountTolerance = 0.10 // écart relatif toléré entre montants d'une même série
	minOccurrences  = 3
	minRegularity   = 0.6 // part des intervalles conformes à la période
	minScore        = 40  // score minimal d'une série proposée
	historyYears    = 3   // profondeur d'historique examinée
)

// candidatePeriods sont les périodicités recherchées, de la plus courte à
// la plus longue.
var candidatePeriods = []calendar.Period{
	calendar.Weekly, calendar.Monthly, calendar.Bimonthly, calendar.Quarterly, calendar.Semiannual, calendar.Yearly,
}

// Op est une opération de l'historique, telle que la détection l'examine.
type Op struct {
	AccountID  int64
	Date       calendar.Date
	Label      string
	Amount     int64
	CategoryID int64
}

// Candidate est une série d'opérations qui se répète régulièrement.
type Candidate struct {
	Signature   string
	AccountID   int64
	Label       string // libellé de la dernière occurrence
	Amount      int64  // montant fixe, ou moyenne des trois dernières occurrences
	Estimated   bool   // montants variables
	Period      calendar.Period
	LastDay     bool          // toujours le dernier jour du mois
	Last        calendar.Date // dernière occurrence, point de départ de la récurrence
	Next        calendar.Date
	First       calendar.Date
	Occurrences int
	Score       int // confiance, de 0 à 100
	CategoryID  int64
}

// Signature identifie une série : compte, sens et libellé sans références
// ni dates (mots contenant un chiffre), casse et accents ignorés.
func Signature(accountID, amount int64, label string) string {
	sign := '-'
	if amount > 0 {
		sign = '+'
	}
	words := strings.FieldsFunc(text.Fold(label), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	words = slices.DeleteFunc(words, func(w string) bool { return strings.ContainsFunc(w, unicode.IsDigit) })
	return fmt.Sprintf("%d|%c|%s", accountID, sign, strings.Join(words, " "))
}

// Detect cherche les séries régulières dans l'historique, au jour today.
// Les opérations d'un même compte, d'un même sens et d'un même libellé
// normalisé forment un groupe ; un groupe se découpe en séries de montants
// proches (±10 %) ; une série retenue a au moins trois occurrences, une
// périodicité respectée à ±3 jours et n'est pas interrompue.
func Detect(ops []Op, today calendar.Date) []Candidate {
	groups := map[string][]Op{}
	for _, op := range ops {
		if op.Amount != 0 {
			sig := Signature(op.AccountID, op.Amount, op.Label)
			groups[sig] = append(groups[sig], op)
		}
	}
	var out []Candidate
	for sig, group := range groups {
		if len(group) < minOccurrences {
			continue
		}
		for _, series := range amountClusters(group) {
			if c, ok := analyse(series, today); ok {
				c.Signature = sig
				out = append(out, c)
			}
		}
	}
	slices.SortFunc(out, func(a, b Candidate) int {
		return cmp.Or(cmp.Compare(b.Score, a.Score), cmp.Compare(a.Next, b.Next), strings.Compare(a.Signature, b.Signature))
	})
	return out
}

// amountClusters découpe un groupe en séries de montants proches : tous à
// ±10 % d'une même valeur centrale, soit un rapport max/min d'au plus
// 1,1/0,9. Chaque série est triée par date.
func amountClusters(ops []Op) [][]Op {
	ops = slices.Clone(ops)
	slices.SortFunc(ops, func(a, b Op) int { return cmp.Compare(abs(a.Amount), abs(b.Amount)) })
	var clusters [][]Op
	for _, op := range ops {
		n := len(clusters)
		if n > 0 && float64(abs(op.Amount)) <= float64(abs(clusters[n-1][0].Amount))*(1+amountTolerance)/(1-amountTolerance) {
			clusters[n-1] = append(clusters[n-1], op)
			continue
		}
		clusters = append(clusters, []Op{op})
	}
	for _, c := range clusters {
		slices.SortStableFunc(c, func(a, b Op) int { return strings.Compare(string(a.Date), string(b.Date)) })
	}
	return clusters
}

// analyse évalue une série triée par date.
func analyse(series []Op, today calendar.Date) (Candidate, bool) {
	n := len(series)
	if n < minOccurrences {
		return Candidate{}, false
	}
	period, regularity := bestPeriod(series)
	if regularity < minRegularity {
		return Candidate{}, false
	}
	first, last := series[0], series[n-1]
	lastDay := period.Unit == calendar.Month && !slices.ContainsFunc(series, func(o Op) bool { return !o.Date.IsLastDayOfMonth() })
	// Série interrompue : deux échéances manquées.
	if period.Nth(last.Date, 2, lastDay).AddDays(dateTolerance) < today {
		return Candidate{}, false
	}
	// Une échéance en retard de moins de trois jours (délai bancaire) reste
	// la prochaine attendue.
	next, _ := period.NextOnOrAfter(last.Date, lastDay, max(today.AddDays(-dateTolerance), last.Date.AddDays(1)))

	amount, estimated, stability := seriesAmount(series)
	countFactor := min(1, 0.5+float64(n-minOccurrences)/6)
	score := int(math.Round(100 * regularity * countFactor * stability))
	if score < minScore {
		return Candidate{}, false
	}
	return Candidate{
		AccountID: last.AccountID, Label: last.Label, Amount: amount, Estimated: estimated,
		Period: period, LastDay: lastDay, Last: last.Date, Next: next, First: first.Date,
		Occurrences: n, Score: score, CategoryID: commonCategory(series),
	}, true
}

// bestPeriod renvoie la périodicité la mieux respectée et sa régularité.
// Seules les périodicités proches de l'écart médian entre occurrences sont
// évaluées (la médiane résiste à une échéance manquée).
func bestPeriod(series []Op) (calendar.Period, float64) {
	gaps := make([]int, len(series)-1)
	for i := range gaps {
		gaps[i] = series[i].Date.DaysUntil(series[i+1].Date)
	}
	slices.Sort(gaps)
	median := float64(gaps[len(gaps)/2])

	best, bestRatio := calendar.Period{}, 0.0
	for _, p := range candidatePeriods {
		if days := p.Days(); median < days*0.5 || median > days*1.5 {
			continue
		}
		if ratio := regularity(series, p); ratio > bestRatio {
			best, bestRatio = p, ratio
		}
	}
	return best, bestRatio
}

// regularity mesure, de 0 à 1, le respect d'une périodicité. Chaque date est
// comparée à l'échéance attendue d'une série ancrée sur la première
// occurrence, recalée de l'écart médian (la première occurrence peut
// elle-même être décalée) : elle est conforme à ±3 jours. Le résultat est la
// plus faible des deux parts : occurrences conformes parmi les occurrences,
// et parmi les échéances attendues (une échéance manquée compte).
func regularity(series []Op, p calendar.Period) float64 {
	first, last := series[0].Date, series[len(series)-1].Date
	rank := func(anchor, d calendar.Date) int {
		return int(math.Round(float64(anchor.DaysUntil(d)) / p.Days()))
	}
	devs := make([]int, len(series))
	for i, o := range series {
		devs[i] = p.Nth(first, rank(first, o.Date), false).DaysUntil(o.Date)
	}
	slices.Sort(devs)
	anchor := first.AddDays(devs[len(devs)/2])

	good, seen := 0, map[int]bool{}
	for _, o := range series {
		k := rank(anchor, o.Date)
		if d := p.Nth(anchor, k, false).DaysUntil(o.Date); !seen[k] && d >= -dateTolerance && d <= dateTolerance {
			good++
			seen[k] = true
		}
	}
	expected := rank(first, last) + 1
	return min(float64(good)/float64(len(series)), float64(good)/float64(max(expected, 1)))
}

// seriesAmount renvoie le montant à prévoir, s'il est estimé, et un facteur
// de stabilité (1 pour un montant fixe, plus faible s'il varie).
func seriesAmount(series []Op) (amount int64, estimated bool, stability float64) {
	fixed := !slices.ContainsFunc(series, func(o Op) bool { return o.Amount != series[0].Amount })
	if fixed {
		return series[0].Amount, false, 1
	}
	recent := series[max(len(series)-3, 0):]
	var sum int64
	for _, o := range recent {
		sum += o.Amount
	}
	amount = int64(math.Round(float64(sum) / float64(len(recent))))

	var mean, variance float64
	for _, o := range series {
		mean += float64(o.Amount)
	}
	mean /= float64(len(series))
	for _, o := range series {
		variance += math.Pow(float64(o.Amount)-mean, 2)
	}
	cv := math.Sqrt(variance/float64(len(series))) / math.Abs(mean)
	return amount, true, max(0.7, 1-cv)
}

// commonCategory renvoie la catégorie la plus fréquente de la série.
func commonCategory(series []Op) int64 {
	counts := map[int64]int{}
	var best int64
	for _, o := range series {
		if o.CategoryID == 0 {
			continue
		}
		counts[o.CategoryID]++
		if c := counts[o.CategoryID]; c > counts[best] || c == counts[best] && o.CategoryID < best {
			best = o.CategoryID
		}
	}
	return best
}

func abs(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}
