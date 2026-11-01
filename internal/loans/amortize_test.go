package loans

import (
	"testing"

	"github.com/codecolors/buddyget/internal/platform/calendar"
)

func checkCoherent(t *testing.T, p Params, s Schedule) {
	t.Helper()
	var capital int64
	for _, r := range s.Rows {
		capital += r.Capital + r.Prepaid
		if r.Payment != r.Interest+r.Capital {
			t.Errorf("échéance %d : %d ≠ %d + %d", r.N, r.Payment, r.Interest, r.Capital)
		}
	}
	if capital != p.Principal || s.Rows[len(s.Rows)-1].Remaining != 0 {
		t.Errorf("capital remboursé %d sur %d, reste %d", capital, p.Principal, s.Rows[len(s.Rows)-1].Remaining)
	}
}

func TestAmortizeMortgage(t *testing.T) {
	// 200 000 € à 3,6 % sur 20 ans : 1 170,22 € par mois.
	p := Params{Principal: 200_000_00, Rate: 3_600, Months: 240, Insurance: 30_00, First: "2026-01-31"}
	s := Amortize(p, nil)
	if s.Payment != 1_170_22 || len(s.Rows) != 240 || s.End != "2045-12-31" {
		t.Fatalf("mensualité %d, %d échéances, fin %s", s.Payment, len(s.Rows), s.End)
	}
	first := s.Rows[0]
	if first.Interest != 600_00 || first.Capital != 570_22 || first.Remaining != 199_429_78 || s.Rows[1].Date != "2026-02-28" {
		t.Errorf("première échéance : %+v", first)
	}
	// Intérêts totaux : 240 mensualités moins le capital, à l'arrondi de la
	// dernière échéance près.
	if want := int64(240*1_170_22 - 200_000_00); s.TotalInterest < want-100 || s.TotalInterest > want+100 {
		t.Errorf("intérêts totaux : %d, attendu ≈ %d", s.TotalInterest, want)
	}
	if s.TotalInsurance != 240*30_00 {
		t.Errorf("assurance totale : %d", s.TotalInsurance)
	}
	checkCoherent(t, p, s)

	st := s.StatusAt(p.Principal, "2026-02-28")
	if st.Paid != 2 || st.Remaining != s.Rows[1].Remaining || st.Next.N != 3 || st.InterestPaid != first.Interest+s.Rows[1].Interest || st.Done {
		t.Errorf("situation : %+v", st)
	}
	if st := s.StatusAt(p.Principal, "2025-12-01"); st.Remaining != p.Principal || st.Paid != 0 {
		t.Errorf("avant la première échéance : %+v", st)
	}
	if !s.StatusAt(p.Principal, "2046-01-01").Done {
		t.Error("crédit soldé")
	}
}

func TestAmortizeZeroRate(t *testing.T) {
	p := Params{Principal: 1_000_00, Months: 3, First: "2026-11-05"}
	s := Amortize(p, nil)
	if s.Payment != 333_34 || s.Rows[2].Payment != 333_32 || s.TotalInterest != 0 {
		t.Errorf("taux nul : %+v", s)
	}
	checkCoherent(t, p, s)
}

func TestPrepayments(t *testing.T) {
	p := Params{Principal: 10_000_00, Months: 10, First: "2026-01-10"}
	// 3 000 € remboursés avant la 4e échéance : même mensualité, 3 de moins.
	s := Amortize(p, []Prepayment{{Date: "2026-04-01", Amount: 3_000_00}})
	if len(s.Rows) != 7 || s.Rows[3].Prepaid != 3_000_00 || s.End != "2026-07-10" || s.TotalPrepaid != 3_000_00 {
		t.Errorf("remboursement partiel : %d échéances, fin %s, %+v", len(s.Rows), s.End, s.Rows[3])
	}
	checkCoherent(t, p, s)

	// Remboursement du solde : le crédit s'arrête.
	s = Amortize(p, []Prepayment{{Date: "2026-03-01", Amount: 50_000_00}})
	if len(s.Rows) != 3 || s.Rows[2].Prepaid != 8_000_00 || s.Rows[2].Payment != 0 || s.Rows[2].Insurance != 0 || s.End != "2026-03-01" {
		t.Errorf("remboursement total : %+v", s.Rows)
	}
	checkCoherent(t, p, s)

	// Avec intérêts, le remboursement anticipé fait aussi baisser leur total.
	with := Params{Principal: 100_000_00, Rate: 4_000, Months: 120, First: "2026-01-15"}
	base, early := Amortize(with, nil), Amortize(with, []Prepayment{{Date: "2027-01-01", Amount: 20_000_00}})
	if len(early.Rows) >= len(base.Rows) || early.TotalInterest >= base.TotalInterest || early.Payment != base.Payment {
		t.Errorf("remboursement anticipé : %d → %d échéances, intérêts %d → %d", len(base.Rows), len(early.Rows), base.TotalInterest, early.TotalInterest)
	}
	checkCoherent(t, with, early)
	if Payment(Params{}) != 0 || Amortize(Params{Principal: 1, Months: 1, First: calendar.Date("2026-01-01")}, nil).End != "2026-01-01" {
		t.Error("cas limites")
	}
}
