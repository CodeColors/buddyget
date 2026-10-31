package money

import (
	"math"
	"testing"

	"github.com/codecolors/buddyget/internal/platform/validation"
)

func TestParse(t *testing.T) {
	for in, want := range map[string]int64{
		"12":               1200,
		"12,5":             1250,
		"12,50":            1250,
		"12.05":            1205,
		",5":               50,
		"-8,99":            -899,
		"+3":               300,
		"\u22124,20":       -420, // signe moins typographique
		"1 234,56":         123456,
		"1\u202f234,56 €":  123456,
		"1.234,56":         123456,
		"1,234.56":         123456,
		"1.234.567,89":     123456789,
		"999 999 999,99":   Max,
		"0":                0,
		"12€50":            1250,
		"  42 ":            4200,
		"1\u00a0000\u00a0": 100000,
	} {
		got, err := Parse(in)
		if err != nil || got != want {
			t.Errorf("Parse(%q) = %d, %v ; attendu %d", in, got, err, want)
		}
	}
}

func TestParseRejects(t *testing.T) {
	for _, in := range []string{"", "abc", "12,345", "1.2.3", "12,", "1,2,3", "--3", "1e3", "1 000 000 000"} {
		_, err := Parse(in)
		if _, ok := validation.Message(err); !ok {
			t.Errorf("Parse(%q) : erreur de saisie attendue, obtenu %v", in, err)
		}
	}
}

func TestFormat(t *testing.T) {
	for in, want := range map[int64]string{
		0:          "0,00\u00a0€",
		5:          "0,05\u00a0€",
		123456:     "1\u202f234,56\u00a0€",
		-899:       "\u22128,99\u00a0€",
		100000000:  "1\u202f000\u202f000,00\u00a0€",
		-123456789: "\u22121\u202f234\u202f567,89\u00a0€",
	} {
		if got := Format(in); got != want {
			t.Errorf("Format(%d) = %q, attendu %q", in, got, want)
		}
	}
	if got := Format(math.MinInt64); got != "\u221292\u202f233\u202f720\u202f368\u202f547\u202f758,08\u00a0€" {
		t.Errorf("Format(MinInt64) = %q", got)
	}
	if FormatSigned(1200) != "+12,00\u00a0€" || FormatSigned(-1200) != "\u221212,00\u00a0€" {
		t.Error("FormatSigned")
	}
	if FormatInput(-123456) != "1234,56" || FormatInput(5) != "0,05" {
		t.Error("FormatInput")
	}
	for in, want := range map[int64]string{
		0: "0\u00a0€", 49: "0\u00a0€", -49: "0\u00a0€", 50: "1\u00a0€", -150: "\u22122\u00a0€", 123456: "1\u202f235\u00a0€",
	} {
		if got := FormatEuros(in); got != want {
			t.Errorf("FormatEuros(%d) = %q, attendu %q", in, got, want)
		}
	}
}

func TestRoundTrip(t *testing.T) {
	for _, v := range []int64{0, 1, 99, 100, 123456, -50, Max, -Max} {
		got, err := Parse(Format(v))
		if err != nil || got != v {
			t.Errorf("Parse(Format(%d)) = %d, %v", v, got, err)
		}
	}
}
