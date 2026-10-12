package money_test

import (
	"errors"
	"math"
	"testing"

	"junglegaming.io/finops-api/internal/domain"
	"junglegaming.io/finops-api/internal/domain/money"
)

func TestParseValid(t *testing.T) {
	m, err := money.Parse("25.00", "BRL")
	if err != nil {
		t.Fatal(err)
	}
	if m.Minor() != 2500 || m.Currency() != "BRL" || m.DecimalString() != "25.00" {
		t.Fatalf("got %+v %s", m.Minor(), m.DecimalString())
	}
}

func TestParseRejects(t *testing.T) {
	cases := []string{"", "NaN", "Infinity", "1e2", "1.0", "1.000", "01.00", "+1.00", "abc"}
	for _, c := range cases {
		if _, err := money.Parse(c, "BRL"); err == nil {
			t.Fatalf("expected error for %q", c)
		}
	}
}

func TestNegativeParse(t *testing.T) {
	m, err := money.Parse("-1.50", "BRL")
	if err != nil {
		t.Fatal(err)
	}
	if !m.IsNegative() || m.DecimalString() != "-1.50" {
		t.Fatalf("got %s", m.DecimalString())
	}
}

func TestCurrencyMismatch(t *testing.T) {
	a, _ := money.Parse("1.00", "BRL")
	b, _ := money.Parse("1.00", "USD")
	if _, err := a.Add(b); !errors.Is(err, domain.ErrCurrencyMismatch) {
		t.Fatalf("got %v", err)
	}
}

func TestOverflow(t *testing.T) {
	a, err := money.FromMinor(math.MaxInt64, "BRL")
	if err != nil {
		t.Fatal(err)
	}
	one, _ := money.FromMinor(1, "BRL")
	if _, err := a.Add(one); !errors.Is(err, domain.ErrOverflow) {
		t.Fatalf("got %v", err)
	}
}

func TestDecimalStringMinInt64(t *testing.T) {
	m, err := money.FromMinor(math.MinInt64, "BRL")
	if err != nil {
		t.Fatal(err)
	}
	if m.DecimalString() == "" || m.DecimalString()[0] != '-' {
		t.Fatalf("got %q", m.DecimalString())
	}
}

func TestZeroPolicy(t *testing.T) {
	z, err := money.Zero("BRL")
	if err != nil || !z.IsZero() {
		t.Fatal(err)
	}
}
