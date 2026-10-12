package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"junglegaming.io/finops-api/internal/adapter/wire"
	"junglegaming.io/finops-api/internal/domain"
	"junglegaming.io/finops-api/internal/domain/money"
)

func TestMapDomainStatus(t *testing.T) {
	cases := []struct {
		err  error
		code int
	}{
		{domain.ErrWalletNotFound, http.StatusNotFound},
		{domain.ErrIdempotencyConflict, http.StatusConflict},
		{domain.ErrUnauthorizedProvider, http.StatusForbidden},
		{domain.ErrInvalidAmount, http.StatusBadRequest},
		{domain.ErrOpeningNotAllowed, http.StatusBadRequest},
		{domain.ErrOverflow, http.StatusBadRequest},
		{domain.ErrPayloadConflict, http.StatusConflict},
		{domain.ErrReferenceNotFound, http.StatusUnprocessableEntity},
		{domain.ErrInsufficientFunds, http.StatusUnprocessableEntity},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		mapDomain(rec, tc.err)
		if rec.Code != tc.code {
			t.Fatalf("%v: got %d want %d", tc.err, rec.Code, tc.code)
		}
	}
	if !errors.Is(domain.ErrWalletNotFound, domain.ErrWalletNotFound) {
		t.Fatal("sentinel")
	}
}

func TestMoneyPayloadRoundTrip(t *testing.T) {
	m, err := money.Parse("1000.00", "BRL")
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(wire.FromMoney(m))
	if err != nil {
		t.Fatal(err)
	}
	var p wire.Money
	if err := json.Unmarshal(b, &p); err != nil {
		t.Fatal(err)
	}
	back, err := p.Money()
	if err != nil {
		t.Fatal(err)
	}
	eq, err := m.Equal(back)
	if err != nil || !eq {
		t.Fatalf("%v %v", back, err)
	}
}
