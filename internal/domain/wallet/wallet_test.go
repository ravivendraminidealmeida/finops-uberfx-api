package wallet_test

import (
	"errors"
	"testing"
	"time"

	"junglegaming.io/finops-api/internal/domain"
	"junglegaming.io/finops-api/internal/domain/money"
	"junglegaming.io/finops-api/internal/domain/wallet"
)

func TestDebitCredit(t *testing.T) {
	bal, _ := money.Parse("100.00", "BRL")
	w, err := wallet.New("w1", "p1", bal, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if w.Version() != 1 {
		t.Fatalf("version %d", w.Version())
	}
	amt, _ := money.Parse("80.00", "BRL")
	_, after, err := w.Debit(amt, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if after.DecimalString() != "20.00" || w.Version() != 2 {
		t.Fatalf("after %s v=%d", after.DecimalString(), w.Version())
	}
	_, _, err = w.Debit(amt, time.Now())
	if !errors.Is(err, domain.ErrInsufficientFunds) {
		t.Fatalf("got %v", err)
	}
	if w.Balance().DecimalString() != "20.00" {
		t.Fatalf("balance mutated on failed debit")
	}
}

func TestRehydrateDoesNotMutate(t *testing.T) {
	bal, _ := money.Parse("50.00", "BRL")
	w, err := wallet.Rehydrate("w1", "p1", bal, 4, time.Now(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if w.Version() != 4 || w.Balance().DecimalString() != "50.00" {
		t.Fatalf("rehydrate applied transitions")
	}
}

func TestRejectNegativeInitial(t *testing.T) {
	bal, _ := money.Parse("-1.00", "BRL")
	if _, err := wallet.New("w1", "p1", bal, time.Now()); err == nil {
		t.Fatal("expected error")
	}
}
