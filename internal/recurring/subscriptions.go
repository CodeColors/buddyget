package recurring

import (
	"cmp"
	"context"
	"math"
	"slices"
	"strings"
	"unicode"

	"github.com/codecolors/buddyget/internal/platform/calendar"
	"github.com/codecolors/buddyget/internal/platform/text"
)

// Paramètres de la vue Abonnements.
const (
	unusedDays       = 90   // sans autre dépense de la catégorie depuis, un abonnement est peut-être oublié
	duplicateSpread  = 0.10 // écart relatif de montant de deux abonnements de même catégorie
	minMerchantChars = 4    // longueur minimale d'un mot désignant le commerçant
)

// genericWords n'identifient pas un commerçant.
var genericWords = map[string]bool{
	"prlv": true, "sepa": true, "prelevement": true, "carte": true, "paiement": true, "abonnement": true,
	"facture": true, "echeance": true, "mensuel": true, "mensualite": true, "virement": true, "europe": true,
}

// Subscription est un abonnement actif : une récurrence marquée comme telle.
type Subscription struct {
	Rule
	Next    calendar.Date // prochain prélèvement
	Monthly int64         // coût mensuel équivalent (positif pour une dépense)
	// Duplicates nomme les autres abonnements du même commerçant, ou de même
	// catégorie et de montant proche : un doublon possible.
	Duplicates []string
	// Unused signale qu'aucune autre dépense de sa catégorie n'a eu lieu
	// depuis 90 jours : l'abonnement sert-il encore ?
	Unused bool
}

// Annual renvoie le coût annuel équivalent.
func (s Subscription) Annual() int64 { return s.Monthly * 12 }

// MonthlyCost ramène le montant d'une récurrence à un coût mensuel.
func MonthlyCost(r Rule) int64 {
	return int64(math.Round(float64(-r.Amount) * 365.25 / 12 / r.Period.Days()))
}

// Subscriptions renvoie les abonnements actifs et non terminés, du plus
// coûteux au moins coûteux.
func (s *Service) Subscriptions(ctx context.Context, today calendar.Date) ([]Subscription, error) {
	rules, err := s.Rules(ctx)
	if err != nil {
		return nil, err
	}
	var out []Subscription
	for _, r := range rules {
		next, ok := r.Next(today)
		if !r.Subscription || !r.Active || !ok {
			continue
		}
		out = append(out, Subscription{Rule: r, Next: next.Due, Monthly: MonthlyCost(r)})
	}
	markDuplicates(out)
	if err := s.markUnused(ctx, out, today); err != nil {
		return nil, err
	}
	slices.SortFunc(out, func(a, b Subscription) int {
		return cmp.Or(cmp.Compare(b.Monthly, a.Monthly), strings.Compare(a.Label, b.Label))
	})
	return out, nil
}

// markDuplicates rapproche les abonnements d'un même commerçant (un mot
// significatif commun dans le libellé), ou de même catégorie et de montant
// proche.
func markDuplicates(subs []Subscription) {
	words := make([]map[string]bool, len(subs))
	for i, s := range subs {
		words[i] = merchantWords(s.Label)
	}
	for i := range subs {
		for j := range subs {
			if i == j {
				continue
			}
			a, b := subs[i], subs[j]
			sameMerchant := false
			for w := range words[i] {
				sameMerchant = sameMerchant || words[j][w]
			}
			close := a.CategoryID != 0 && a.CategoryID == b.CategoryID &&
				math.Abs(float64(a.Monthly-b.Monthly)) <= duplicateSpread*float64(max(a.Monthly, b.Monthly))
			if sameMerchant || close {
				subs[i].Duplicates = append(subs[i].Duplicates, b.Label)
			}
		}
	}
}

// merchantWords renvoie les mots d'un libellé susceptibles de désigner le
// commerçant : sans chiffre, assez longs, hors termes bancaires.
func merchantWords(label string) map[string]bool {
	out := map[string]bool{}
	for _, w := range strings.FieldsFunc(text.Fold(label), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if len(w) >= minMerchantChars && !genericWords[w] && !strings.ContainsFunc(w, unicode.IsDigit) {
			out[w] = true
		}
	}
	return out
}

// markUnused signale les abonnements catégorisés, ouverts depuis plus de
// 90 jours, sans autre dépense de leur catégorie sur cette période.
func (s *Service) markUnused(ctx context.Context, subs []Subscription, today calendar.Date) error {
	since := today.AddDays(-unusedDays)
	rows, err := s.db.R.QueryContext(ctx, `
		SELECT account_id, label, amount, category_id FROM transactions
		WHERE category_id IS NOT NULL AND transfer_id IS NULL AND amount < 0 AND date > ? AND date <= ?`, since, today)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	// Séries des dépenses récentes, par catégorie.
	recent := map[int64]map[string]bool{}
	for rows.Next() {
		var account, amount, category int64
		var label string
		if err := rows.Scan(&account, &label, &amount, &category); err != nil {
			return err
		}
		if recent[category] == nil {
			recent[category] = map[string]bool{}
		}
		recent[category][Signature(account, amount, label)] = true
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for i, sub := range subs {
		if sub.CategoryID == 0 || sub.Start > since {
			continue
		}
		others := len(recent[sub.CategoryID])
		if recent[sub.CategoryID][sub.Signature] {
			others--
		}
		subs[i].Unused = others == 0
	}
	return nil
}
