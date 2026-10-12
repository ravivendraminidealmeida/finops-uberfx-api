package auth

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"

	"junglegaming.io/finops-api/internal/platform/config"
)

type Identity struct {
	Subject    string
	ClientID   string
	ProviderID string
	Internal   bool
}

type contextKey struct{}

func FromContext(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(contextKey{}).(Identity)
	return id, ok
}

func withIdentity(ctx context.Context, id Identity) context.Context {
	return context.WithValue(ctx, contextKey{}, id)
}

type Verifier struct {
	verifier         *oidc.IDTokenVerifier
	internalClientID string
}

func NewVerifier(ctx context.Context, cfg config.Config) (*Verifier, error) {
	provider, err := oidc.NewProvider(ctx, cfg.OIDCIssuer)
	if err != nil {
		return nil, fmt.Errorf("oidc provider: %w", err)
	}
	return &Verifier{
		verifier: provider.Verifier(&oidc.Config{
			ClientID: cfg.OIDCAudience,
		}),
		internalClientID: cfg.InternalClientID,
	}, nil
}

func (v *Verifier) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")
		if !strings.HasPrefix(header, "Bearer ") {
			http.Error(w, `{"error":"unauthorized","message":"missing bearer token"}`, http.StatusUnauthorized)
			return
		}
		token := strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
		if token == "" {
			http.Error(w, `{"error":"unauthorized","message":"missing bearer token"}`, http.StatusUnauthorized)
			return
		}
		idtok, err := v.verifier.Verify(r.Context(), token)
		if err != nil {
			http.Error(w, `{"error":"unauthorized","message":"invalid or expired token"}`, http.StatusUnauthorized)
			return
		}
		var claims struct {
			Azp        string `json:"azp"`
			ClientID   string `json:"client_id"`
			ProviderID string `json:"provider_id"`
			Sub        string `json:"sub"`
		}
		if err := idtok.Claims(&claims); err != nil {
			http.Error(w, `{"error":"unauthorized","message":"invalid claims"}`, http.StatusUnauthorized)
			return
		}
		clientID := claims.Azp
		if clientID == "" {
			clientID = claims.ClientID
		}
		ident := Identity{Subject: claims.Sub, ClientID: clientID, ProviderID: claims.ProviderID}
		if clientID == v.internalClientID {
			ident.Internal = true
			ident.ProviderID = ""
		} else if ident.ProviderID == "" {
			http.Error(w, `{"error":"unauthorized","message":"provider_id claim is required"}`, http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r.WithContext(withIdentity(r.Context(), ident)))
	})
}
