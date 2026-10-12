//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"junglegaming.io/finops-api/internal/adapter/auth"
	httpapi "junglegaming.io/finops-api/internal/adapter/http"
	"junglegaming.io/finops-api/internal/platform/config"
	"junglegaming.io/finops-api/internal/platform/obs"
)

func TestHTTPAuthenticatedFlow(t *testing.T) {
	_, _, uow := startPostgres(t)
	svc := newSvc(uow)
	issuer := keycloakIssuer(t)
	verifier, err := auth.NewVerifier(t.Context(), config.Config{
		OIDCIssuer:       issuer,
		OIDCAudience:     "finops-api",
		InternalClientID: "wallet-internal",
	})
	if err != nil {
		t.Fatal(err)
	}
	api := httptest.NewServer(httpapi.NewServer(svc, verifier, func(context.Context) error { return nil }, obs.NewMetrics(), zap.NewNop()).Router())
	t.Cleanup(api.Close)

	internalTok := clientToken(t, issuer, "wallet-internal", "dummy_wallet-internal")
	providerA := clientToken(t, issuer, "provider-a", "dummy_provider-a")
	providerB := clientToken(t, issuer, "provider-b", "dummy_provider-b")

	playerID := uuid.NewString()
	openBody, _ := json.Marshal(map[string]any{
		"playerId":       playerID,
		"initialBalance": map[string]string{"amount": "100.00", "currency": "BRL"},
	})
	openRes := mustJSON(t, api.URL+"/wallets", http.MethodPost, internalTok, "", openBody, http.StatusCreated)
	walletID := openRes["id"].(string)

	betBody, _ := json.Marshal(map[string]any{
		"providerId":            "provider-a",
		"externalTransactionId": "http-bet-1",
		"playerId":              playerID,
		"walletId":              walletID,
		"roundId":               "round-1",
		"gameId":                "fortune-chimp",
		"kind":                  "BET",
		"money":                 map[string]string{"amount": "10.00", "currency": "BRL"},
	})
	betRes := mustJSON(t, api.URL+"/wagering/transactions", http.MethodPost, providerA, "provider-a:http-bet-1", betBody, http.StatusOK)
	if betRes["status"] != "PROCESSED" {
		t.Fatalf("bet status %+v", betRes)
	}

	got := mustJSON(t, api.URL+"/wallets/"+walletID, http.MethodGet, internalTok, "", nil, http.StatusOK)
	bal := got["balance"].(map[string]any)
	if bal["amount"] != "90.00" {
		t.Fatalf("balance %+v", got)
	}

	rec := doHTTP(t, api.URL+"/wagering/transactions", http.MethodPost, providerB, "provider-b:http-bet-1", betBody)
	if rec.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-provider bet: %d %s", rec.StatusCode, rec.Body)
	}

	missing := doHTTP(t, api.URL+"/wallets/"+walletID, http.MethodGet, "", "", nil)
	if missing.StatusCode != http.StatusUnauthorized {
		t.Fatalf("missing token: %d %s", missing.StatusCode, missing.Body)
	}
	invalid := doHTTP(t, api.URL+"/wallets/"+walletID, http.MethodGet, "not-a-jwt", "", nil)
	if invalid.StatusCode != http.StatusUnauthorized {
		t.Fatalf("invalid token: %d %s", invalid.StatusCode, invalid.Body)
	}

	expired := doHTTP(t, api.URL+"/wallets/"+walletID, http.MethodGet, expiredJWT, "", nil)
	if expired.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expired token: %d %s", expired.StatusCode, expired.Body)
	}
}

// expiredJWT is a structurally valid RS256 JWT whose exp is in 2010. Signature is not from Keycloak.
const expiredJWT = "eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9.eyJleHAiOjEyNjIzMDQwMDAsImlhdCI6MTI2MjMwMDAwMCwiaXNzIjoiaHR0cDovL2V4YW1wbGUiLCJhdWQiOiJmaW5vcHMtYXBpIn0.e30"

type httpResult struct {
	StatusCode int
	Body       string
}

func doHTTP(t *testing.T, url, method, token, idempotency string, body []byte) httpResult {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, url, rdr)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")
	if idempotency != "" {
		req.Header.Set("Idempotency-Key", idempotency)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	return httpResult{StatusCode: res.StatusCode, Body: string(raw)}
}

func mustJSON(t *testing.T, url, method, token, idempotency string, body []byte, want int) map[string]any {
	t.Helper()
	rec := doHTTP(t, url, method, token, idempotency, body)
	if rec.StatusCode != want {
		t.Fatalf("%s %s: status %d want %d body %s", method, url, rec.StatusCode, want, rec.Body)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(rec.Body), &out); err != nil {
		t.Fatal(err)
	}
	return out
}
