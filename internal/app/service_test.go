package app_test

import (
	"context"
	"errors"
	"testing"

	"junglegaming.io/finops-api/internal/app"
	"junglegaming.io/finops-api/internal/domain"
	"junglegaming.io/finops-api/internal/domain/money"
)

func TestProcessWagerRequiresProviderMatch(t *testing.T) {
	svc := app.NewService(nil, app.SystemClock{}, app.UUIDGen{}, app.Config{})
	amt, _ := money.Parse("10.00", "BRL")
	_, err := svc.ProcessWager(context.Background(), app.ProcessWagerCommand{
		ProviderID:            "provider-a",
		AuthenticatedProvider: "provider-b",
		IdempotencyKey:        "k",
		Amount:                amt,
	})
	if !errors.Is(err, domain.ErrUnauthorizedProvider) {
		t.Fatalf("got %v", err)
	}
}
