// Package savings gère les comptes d'épargne : réglages (taux, méthode
// d'intérêts, plafond, blocage), calcul des intérêts et des fonds
// disponibles ou bloqués, inscription des intérêts. Les calculs sont des
// fonctions pures ; le service lit et écrit les données.
package savings

import (
	"slices"

	"github.com/codecolors/buddyget/internal/platform/calendar"
	"github.com/codecolors/buddyget/internal/platform/money"
	"github.com/codecolors/buddyget/internal/platform/validation"
)

// Product est le type de produit d'épargne.
type Product string

const (
	LivretA      Product = "livret_a"
	LDDS         Product = "ldds"
	LEP          Product = "lep"
	PEL          Product = "pel"
	Livret       Product = "livret"
	LifeInsur    Product = "assurance_vie"
	TermDeposit  Product = "cat"
	OtherProduct Product = "autre"
)

// Label renvoie le nom usuel du produit.
func (p Product) Label() string {
	if t, ok := TemplateFor(p); ok {
		return t.Label
	}
	return "Autre"
}

// Method est la méthode de calcul des intérêts.
type Method string

const (
	NoInterest Method = "aucun"
	Simple     Method = "simple"         // au jour le jour, les intérêts ne produisent pas d'intérêts
	Compound   Method = "compose_annuel" // au jour le jour, intérêts capitalisés chaque 31/12
	Fortnight  Method = "quinzaine"      // par quinzaine, capitalisés au 31/12 (livrets réglementés)
)

// Methods liste les méthodes dans l'ordre d'affichage.
var Methods = []Method{Fortnight, Compound, Simple, NoInterest}

// Label décrit la méthode.
func (m Method) Label() string {
	switch m {
	case Simple:
		return "Intérêts simples, au jour le jour"
	case Compound:
		return "Intérêts composés, capitalisés chaque année"
	case Fortnight:
		return "Par quinzaine, capitalisés au 31 décembre"
	}
	return "Aucun intérêt"
}

// Settings sont les réglages d'un compte d'épargne. Les valeurs nulles
// signifient « sans » : pas de taux, de plafond, de blocage ni de pénalité.
type Settings struct {
	AccountID    int64
	Product      Product
	Rate         int // taux annuel en centièmes de point (170 = 1,70 %)
	Method       Method
	AutoInterest bool  // intérêts inscrits automatiquement au 31/12
	Ceiling      int64 // plafond des versements
	// Blocage : tout jusqu'à UnlockDate, ou chaque versement LockMonths mois.
	UnlockDate calendar.Date
	LockMonths int
	Penalty    int // pénalité de retrait anticipé, centièmes de point
	Since      calendar.Date
}

// EarnsInterest indique un compte rémunéré.
func (s Settings) EarnsInterest() bool { return s.Rate > 0 && s.Method != NoInterest }

// Locked indique un compte dont les fonds peuvent être bloqués.
func (s Settings) Locked() bool { return s.UnlockDate != "" || s.LockMonths > 0 }

// PenaltyOn renvoie la pénalité due sur un retrait anticipé.
func (s Settings) PenaltyOn(amount int64) int64 {
	return (max(amount, 0)*int64(s.Penalty) + 5000) / 10000
}

// Validate contrôle les réglages, indépendamment du compte.
func (s Settings) Validate() error {
	switch {
	case !slices.ContainsFunc(Templates, func(t Template) bool { return t.Product == s.Product }):
		return validation.Error("Choisissez le type de produit d'épargne.")
	case !slices.Contains(Methods, s.Method):
		return validation.Error("Choisissez la méthode de calcul des intérêts.")
	case s.Rate < 0 || s.Rate > 10000:
		return validation.Error("Taux invalide : entre 0 et 100 %.")
	case s.Penalty < 0 || s.Penalty > 10000:
		return validation.Error("Pénalité invalide : entre 0 et 100 %.")
	case s.Ceiling < 0 || s.Ceiling > money.Max:
		return validation.Error("Plafond invalide.")
	case s.LockMonths < 0 || s.LockMonths > 600:
		return validation.Error("Durée de blocage invalide : 600 mois au plus.")
	case s.UnlockDate != "" && s.LockMonths > 0:
		return validation.Error("Choisissez une date de déblocage ou une durée, pas les deux.")
	}
	return nil
}

// Template est un modèle de compte d'épargne : des valeurs par défaut,
// modifiables, sans valeur officielle. Un taux ou un plafond inconnu reste
// nul.
type Template struct {
	Product Product
	Label   string
	Rate    int
	Method  Method
	Ceiling int64
	// Note précise la portée des valeurs (taux fixé à l'ouverture…).
	Note string
}

// TemplatesCheckedOn est la date à laquelle les taux et plafonds des
// modèles ont été relevés ; RatesSince, celle depuis laquelle les taux des
// livrets réglementés s'appliquent.
const (
	TemplatesCheckedOn calendar.Date = "2026-10-31"
	RatesSince         calendar.Date = "2026-08-01"
)

// Templates liste les modèles proposés à la création d'un compte.
var Templates = []Template{
	{Product: LivretA, Label: "Livret A", Rate: 170, Method: Fortnight, Ceiling: 22_950_00},
	{Product: LDDS, Label: "LDDS", Rate: 170, Method: Fortnight, Ceiling: 12_000_00},
	{Product: LEP, Label: "LEP", Rate: 250, Method: Fortnight, Ceiling: 10_000_00, Note: "Sous conditions de revenus."},
	{Product: PEL, Label: "PEL", Rate: 200, Method: Compound, Ceiling: 61_200_00,
		Note: "Taux des plans ouverts depuis le 1er janvier 2026, fixé à l'ouverture : reprenez celui de votre contrat."},
	{Product: Livret, Label: "Livret bancaire", Method: Fortnight, Note: "Taux propre à chaque banque."},
	{Product: LifeInsur, Label: "Assurance-vie", Method: Compound, Note: "Rendement annuel variable, connu après coup."},
	{Product: TermDeposit, Label: "Compte à terme", Method: Simple, Note: "Indiquez la date d'échéance comme date de déblocage."},
	{Product: OtherProduct, Label: "Autre", Method: NoInterest},
}

// TemplateFor renvoie le modèle d'un produit.
func TemplateFor(p Product) (Template, bool) {
	i := slices.IndexFunc(Templates, func(t Template) bool { return t.Product == p })
	if i < 0 {
		return Template{}, false
	}
	return Templates[i], true
}

// Settings renvoie les réglages par défaut du modèle.
func (t Template) Settings() Settings {
	return Settings{Product: t.Product, Rate: t.Rate, Method: t.Method, Ceiling: t.Ceiling, AutoInterest: true}
}
