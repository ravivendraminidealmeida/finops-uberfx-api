package ledger_test

import (
	"errors"
	"testing"
	"time"

	"junglegaming.io/finops-api/internal/domain"
	"junglegaming.io/finops-api/internal/domain/ledger"
	"junglegaming.io/finops-api/internal/domain/money"
)

func TestLedgerInvariant(t *testing.T) {
	amt, _ := money.Parse("10.00", "BRL")
	before, _ := money.Parse("100.00", "BRL")
	after, _ := money.Parse("90.00", "BRL")
	if _, err := ledger.New("e", "w", "t", ledger.Debit, amt, before, after, time.Now()); err != nil {
		t.Fatal(err)
	}
	wrong, _ := money.Parse("80.00", "BRL")
	if _, err := ledger.New("e", "w", "t", ledger.Debit, amt, before, wrong, time.Now()); !errors.Is(err, domain.ErrLedgerInvariant) {
		t.Fatalf("got %v", err)
	}
}
