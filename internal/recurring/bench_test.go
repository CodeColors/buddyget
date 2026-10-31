package recurring

import (
	"fmt"
	"testing"

	"github.com/codecolors/buddyget/internal/platform/calendar"
)

// BenchmarkDetect cherche les séries parmi 20 000 opérations sur trois ans :
// 60 séries mensuelles mêlées à des achats sans régularité.
func BenchmarkDetect(b *testing.B) {
	var ops []Op
	for s := range 60 {
		for i := range 36 {
			ops = append(ops, Op{AccountID: 1, Date: calendar.Monthly.Nth("2023-11-01", i, false).AddDays(s % 28),
				Label: fmt.Sprintf("PRLV FOURNISSEUR %c%c ECH/%d", 'A'+s%26, 'A'+s/26, i), Amount: -int64(1000 + s*37)})
		}
	}
	for i := len(ops); i < 20_000; i++ {
		ops = append(ops, Op{AccountID: 1, Date: calendar.Date("2023-11-01").AddDays(i % 1095),
			Label: fmt.Sprintf("CB COMMERCE %c%c", 'A'+i%26, 'A'+i/26%26), Amount: -int64(100 + i%9000)})
	}
	for b.Loop() {
		if cs := Detect(ops, "2026-10-31"); len(cs) < 60 {
			b.Fatalf("%d séries détectées", len(cs))
		}
	}
}
