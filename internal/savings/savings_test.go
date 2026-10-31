package savings

import (
	"slices"
	"testing"

	"github.com/codecolors/buddyget/internal/platform/calendar"
	"github.com/codecolors/buddyget/internal/platform/validation"
)

func TestFortnightInterest(t *testing.T) {
	st := Settings{Rate: 300, Method: Fortnight}
	start := Start{Balance: 10_000_00}
	moves := []Move{
		{Date: "2026-06-20", Amount: -400_00},  // retrait : perd la quinzaine du 16 juin
		{Date: "2026-03-10", Amount: 1_000_00}, // versement : compte à partir du 16 mars
		{Date: "2026-12-20", Amount: 5_000_00}, // compterait à partir de janvier
		{Date: "2025-12-31", Amount: 99_00},    // autre année : ignoré
	}
	// 10 000 × 3 % + 1 000 × 3 %/24 × 19 − 400 × 3 %/24 × 13
	if got := Interest(st, start, moves, "2026-12-31"); got != 317_25 {
		t.Errorf("intérêts de l'année : %d, attendu 31725", got)
	}
	// Au 30 juin : douze quinzaines achevées.
	if got := Interest(st, start, moves, "2026-06-30"); got != 158_25 {
		t.Errorf("intérêts au 30 juin : %d, attendu 15825", got)
	}
	// Le 29 juin, la quinzaine du 16 n'est pas achevée.
	if got := Interest(st, start, moves, "2026-06-29"); got != 158_25-12_50-1_25+50 {
		t.Errorf("intérêts au 29 juin : %d", got)
	}
	// Un versement le 1er compte dès le 16, un versement le 16 dès le 1er.
	for date, want := range map[calendar.Date]int64{"2026-01-01": 11_50, "2026-01-16": 11_00} {
		if got := Interest(Settings{Rate: 1200, Method: Fortnight}, Start{}, []Move{{Date: date, Amount: 100_00}}, "2026-12-31"); got != want {
			t.Errorf("versement du %s : %d, attendu %d", date, got, want)
		}
	}
}

func TestDailyInterest(t *testing.T) {
	start := Start{Balance: 10_000_00, Interest: 500_00}
	moves := []Move{{Date: "2026-07-01", Amount: 3_650_00}}
	// Le versement du 1er juillet rapporte du 2 juillet au 31 décembre :
	// 183 jours, 3 650 × 2 % × 183/365 = 36,60 €.
	if got := Interest(Settings{Rate: 200, Method: Compound}, start, moves, "2026-12-31"); got != 236_60 {
		t.Errorf("composés : %d, attendu 23660", got)
	}
	// En intérêts simples, les 500 € d'intérêts passés ne rapportent rien.
	if got := Interest(Settings{Rate: 200, Method: Simple}, start, moves, "2026-12-31"); got != 226_60 {
		t.Errorf("simples : %d, attendu 22660", got)
	}
	// Année bissextile : 366 jours au taux annuel.
	if got := Interest(Settings{Rate: 200, Method: Compound}, Start{Balance: 10_000_00}, nil, "2028-12-31"); got != 200_00 {
		t.Errorf("année bissextile : %d", got)
	}
	if got := Interest(Settings{Rate: 200, Method: Compound}, Start{Balance: 10_000_00}, nil, "2026-01-31"); got != 16_99 {
		t.Errorf("un mois : %d", got)
	}
	for _, st := range []Settings{{Method: Compound}, {Rate: 200, Method: NoInterest}} {
		if got := Interest(st, start, moves, "2026-12-31"); got != 0 {
			t.Errorf("%+v : %d", st, got)
		}
	}
}

func TestAvailability(t *testing.T) {
	byDate := Settings{UnlockDate: "2027-06-30"}
	moves := []Move{{Date: "2026-01-01", Amount: 1_000_00}}
	if a := AvailabilityOn(byDate, moves, "2026-10-31"); a != (Availability{1_000_00, 0, 1_000_00, "2027-06-30"}) {
		t.Errorf("bloqué jusqu'au 30/06/2027 : %+v", a)
	}
	if a := AvailabilityOn(byDate, moves, "2027-06-30"); a != (Availability{1_000_00, 1_000_00, 0, ""}) {
		t.Errorf("débloqué : %+v", a)
	}

	rolling := Settings{LockMonths: 12}
	moves = []Move{
		{Date: "2025-09-15", Amount: 100_00},
		{Date: "2026-03-10", Amount: 200_00},
		{Date: "2026-05-01", Amount: -50_00},
		{Date: "2026-11-02", Amount: 1_000_00}, // après la date : ignoré
	}
	if a := AvailabilityOn(rolling, moves, "2026-10-31"); a != (Availability{250_00, 50_00, 200_00, "2027-03-10"}) {
		t.Errorf("blocage glissant : %+v", a)
	}
	moves[2].Amount = -150_00 // retrait anticipé : il entame les fonds bloqués
	if a := AvailabilityOn(rolling, moves, "2026-10-31"); a != (Availability{150_00, 0, 150_00, "2027-03-10"}) {
		t.Errorf("après un retrait anticipé : %+v", a)
	}
	if a := AvailabilityOn(Settings{}, moves, "2026-10-31"); a.Available != 150_00 || a.Blocked != 0 {
		t.Errorf("sans blocage : %+v", a)
	}
	if p := (Settings{Penalty: 200}).PenaltyOn(1_000_00); p != 20_00 {
		t.Errorf("pénalité : %d", p)
	}
}

func TestValidateAndTemplates(t *testing.T) {
	for _, tpl := range Templates {
		if err := tpl.Settings().Validate(); err != nil {
			t.Errorf("modèle %s : %v", tpl.Label, err)
		}
	}
	livret, _ := TemplateFor(LivretA)
	for _, st := range []Settings{
		{Product: "inconnu", Method: Fortnight},
		{Product: LivretA, Method: "?"},
		{Product: LivretA, Method: Fortnight, Rate: 10001},
		{Product: LivretA, Method: Fortnight, UnlockDate: "2027-01-01", LockMonths: 6},
		{Product: LivretA, Method: Fortnight, LockMonths: 601},
	} {
		if _, ok := validation.Message(st.Validate()); !ok {
			t.Errorf("réglages invalides acceptés : %+v", st)
		}
	}
	if livret.Settings().Ceiling != 22_950_00 || LivretA.Label() != "Livret A" || Product("x").Label() != "Autre" {
		t.Error("modèle du Livret A")
	}
}

func TestProjectInterest(t *testing.T) {
	st := Settings{Rate: 300, Method: Fortnight}
	moves := []Move{
		{Date: "2025-12-31", Amount: 1_000_00},
		{Date: "2026-12-31", Amount: 30_00, Interest: true}, // 2026 déjà inscrit
	}
	got := ProjectInterest(st, moves, "2026-10-31", "2028-12-31")
	// 2027 : 1 030 × 3 % ; 2028 : (1 030 + 30,90) × 3 %.
	want := []Move{{"2027-12-31", 30_90, true}, {"2028-12-31", 31_83, true}}
	if !slices.Equal(got, want) {
		t.Errorf("intérêts projetés : %+v", got)
	}
	if got := ProjectInterest(st, moves, "2026-10-31", "2027-12-30"); len(got) != 0 {
		t.Errorf("avant le 31 décembre : %+v", got)
	}
	if got := ProjectInterest(Settings{Method: Fortnight}, moves, "2026-10-31", "2028-12-31"); got != nil {
		t.Errorf("sans taux : %+v", got)
	}
}
