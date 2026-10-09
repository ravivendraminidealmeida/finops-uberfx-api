package httpapi

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"junglegaming.io/finops-api/internal/domain"
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
