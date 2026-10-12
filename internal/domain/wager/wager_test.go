package wager_test

import (
	"errors"
	"testing"
	"time"

	"junglegaming.io/finops-api/internal/domain"
	"junglegaming.io/finops-api/internal/domain/money"
	"junglegaming.io/finops-api/internal/domain/wager"
)

func TestParseKindOpening(t *testing.T) {
	_, err := wager.ParseKind("OPENING")
	if !errors.Is(err, domain.ErrOpeningNotAllowed) {
		t.Fatalf("got %v", err)
	}
}

func TestExternalOpeningRejected(t *testing.T) {
	amt, _ := money.Parse("10.00", "BRL")
	_, err := wager.NewExternal(wager.NewExternalParams{
		ID: "t1", Kind: wager.KindOpening, WalletID: "w", PlayerID: "p", Amount: amt,
		ProviderID: "a", ExternalTransactionID: "e", IdempotencyKey: "k", PayloadHash: "h",
		RoundID: "r", GameID: "g", Now: time.Now(),
	})
	if !errors.Is(err, domain.ErrOpeningNotAllowed) {
		t.Fatalf("got %v", err)
	}
}

func TestLossRequiresZero(t *testing.T) {
	amt, _ := money.Parse("1.00", "BRL")
	_, err := wager.NewExternal(wager.NewExternalParams{
		ID: "t1", Kind: wager.KindLoss, WalletID: "w", PlayerID: "p", Amount: amt,
		ProviderID: "a", ExternalTransactionID: "e", IdempotencyKey: "k", PayloadHash: "h",
		RoundID: "r", GameID: "g", Now: time.Now(),
	})
	if !errors.Is(err, domain.ErrInvalidAmount) {
		t.Fatalf("got %v", err)
	}
}

func TestBetRequiresPositive(t *testing.T) {
	amt, _ := money.Parse("0.00", "BRL")
	_, err := wager.NewExternal(wager.NewExternalParams{
		ID: "t1", Kind: wager.KindBet, WalletID: "w", PlayerID: "p", Amount: amt,
		ProviderID: "a", ExternalTransactionID: "e", IdempotencyKey: "k", PayloadHash: "h",
		RoundID: "r", GameID: "g", Now: time.Now(),
	})
	if !errors.Is(err, domain.ErrInvalidAmount) {
		t.Fatalf("got %v", err)
	}
}

func TestWinDoesNotRequireReference(t *testing.T) {
	amt, _ := money.Parse("10.00", "BRL")
	tx, err := wager.NewExternal(wager.NewExternalParams{
		ID: "t1", Kind: wager.KindWin, WalletID: "w", PlayerID: "p", Amount: amt,
		ProviderID: "a", ExternalTransactionID: "e", IdempotencyKey: "k", PayloadHash: "h",
		RoundID: "r", GameID: "g", Now: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if tx.ReferenceExternalID() != "" {
		t.Fatalf("unexpected ref %q", tx.ReferenceExternalID())
	}
}

func TestRefundRequiresReference(t *testing.T) {
	amt, _ := money.Parse("10.00", "BRL")
	_, err := wager.NewExternal(wager.NewExternalParams{
		ID: "t1", Kind: wager.KindRefund, WalletID: "w", PlayerID: "p", Amount: amt,
		ProviderID: "a", ExternalTransactionID: "e", IdempotencyKey: "k", PayloadHash: "h",
		RoundID: "r", GameID: "g", Now: time.Now(),
	})
	if !errors.Is(err, domain.ErrReferenceRequired) {
		t.Fatalf("got %v", err)
	}
}

func TestTerminalTransition(t *testing.T) {
	amt, _ := money.Parse("10.00", "BRL")
	tx, err := wager.NewExternal(wager.NewExternalParams{
		ID: "t1", Kind: wager.KindBet, WalletID: "w", PlayerID: "p", Amount: amt,
		ProviderID: "a", ExternalTransactionID: "e", IdempotencyKey: "k", PayloadHash: "h",
		RoundID: "r", GameID: "g", Now: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.MarkProcessed(amt, "", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := tx.MarkRejected(wager.FailureInsufficientFunds, time.Now()); !errors.Is(err, domain.ErrTerminalTransaction) {
		t.Fatalf("got %v", err)
	}
}
