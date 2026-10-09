package httpapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"junglegaming.io/finops-api/internal/adapter/auth"
	"junglegaming.io/finops-api/internal/app"
	"junglegaming.io/finops-api/internal/domain"
	"junglegaming.io/finops-api/internal/domain/idemp"
	"junglegaming.io/finops-api/internal/domain/money"
	"junglegaming.io/finops-api/internal/domain/wager"
	"junglegaming.io/finops-api/internal/domain/wallet"
)

type HealthChecker interface {
	Ready(ctx context.Context) error
}

type Server struct {
	svc     *app.Service
	auth    *auth.Verifier
	health  HealthChecker
	metrics *Metrics
}

type Metrics struct {
	ReconcileDivergences prometheus.Counter
	Results              *prometheus.CounterVec
}

func NewMetrics() *Metrics {
	m := &Metrics{
		ReconcileDivergences: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "finops_reconciliation_divergences_total",
			Help: "Wallet reconciliation mismatches",
		}),
		Results: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "finops_wager_results_total",
			Help: "Wager processing results by status",
		}, []string{"status", "kind"}),
	}
	_ = prometheus.Register(m.ReconcileDivergences)
	_ = prometheus.Register(m.Results)
	return m
}

func NewServer(svc *app.Service, verifier *auth.Verifier, health HealthChecker, metrics *Metrics) *Server {
	return &Server{svc: svc, auth: verifier, health: health, metrics: metrics}
}

func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)
	r.Get("/health/live", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, HealthResponse{Status: "live"})
	})
	r.Get("/health/ready", s.ready)
	r.Handle("/metrics", promhttp.Handler())

	r.Group(func(r chi.Router) {
		r.Use(s.auth.Middleware)
		r.Post("/wallets", s.openWallet)
		r.Get("/wallets/{walletId}", s.getWallet)
		r.Get("/wallets/{walletId}/ledger", s.listLedger)
		r.Post("/wallets/{walletId}/reconciliation", s.reconcile)
		r.Post("/wagering/transactions", s.processWager)
		r.Get("/wagering/transactions/{transactionId}", s.getTx)
		r.Get("/providers/{providerId}/wagering/transactions/{externalTransactionId}", s.getByExternal)
	})
	return r
}

func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	if s.health != nil {
		if err := s.health.Ready(r.Context()); err != nil {
			writeErr(w, http.StatusServiceUnavailable, "not_ready", err.Error())
			return
		}
	}
	writeJSON(w, http.StatusOK, HealthResponse{Status: "ready"})
}

func (s *Server) openWallet(w http.ResponseWriter, r *http.Request) {
	id, ok := auth.FromContext(r.Context())
	if !ok || !id.Internal {
		writeErr(w, http.StatusForbidden, "forbidden", "wallet opening requires the internal service identity")
		return
	}
	var body OpenWalletRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	amt, err := money.Parse(body.InitialBalance.Amount, body.InitialBalance.Currency)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_amount", err.Error())
		return
	}
	if amt.IsNegative() {
		writeErr(w, http.StatusBadRequest, "invalid_amount", "initial balance cannot be negative")
		return
	}
	res, err := s.svc.OpenWallet(r.Context(), app.OpenWalletCommand{
		PlayerID:        wallet.PlayerID(body.PlayerID),
		Initial:         amt,
		InternalService: true,
	})
	if err != nil {
		mapDomain(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, NewWalletResponse(res.Wallet))
}

func (s *Server) getWallet(w http.ResponseWriter, r *http.Request) {
	wal, err := s.svc.GetWallet(r.Context(), wallet.ID(chi.URLParam(r, "walletId")))
	if err != nil {
		mapDomain(w, err)
		return
	}
	writeJSON(w, http.StatusOK, NewWalletResponse(wal))
}

func (s *Server) listLedger(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	afterTime := time.Time{}
	afterID := ""
	if c := r.URL.Query().Get("cursor"); c != "" {
		raw, err := base64.RawURLEncoding.DecodeString(c)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "invalid_cursor", "cursor is not valid")
			return
		}
		parts := strings.SplitN(string(raw), "|", 2)
		if len(parts) != 2 {
			writeErr(w, http.StatusBadRequest, "invalid_cursor", "cursor is not valid")
			return
		}
		t, err := time.Parse(time.RFC3339Nano, parts[0])
		if err != nil {
			writeErr(w, http.StatusBadRequest, "invalid_cursor", "cursor is not valid")
			return
		}
		afterTime, afterID = t, parts[1]
	}
	items, err := s.svc.ListLedger(r.Context(), wallet.ID(chi.URLParam(r, "walletId")), afterTime, afterID, limit)
	if err != nil {
		mapDomain(w, err)
		return
	}
	out := make([]LedgerEntryResponse, 0, len(items))
	var next string
	for _, e := range items {
		item := NewLedgerEntryResponse(e)
		out = append(out, item)
		next = base64.RawURLEncoding.EncodeToString([]byte(item.CreatedAt + "|" + item.ID))
	}
	writeJSON(w, http.StatusOK, LedgerListResponse{Items: out, NextCursor: next})
}

func (s *Server) reconcile(w http.ResponseWriter, r *http.Request) {
	res, err := s.svc.Reconcile(r.Context(), wallet.ID(chi.URLParam(r, "walletId")))
	if err != nil {
		mapDomain(w, err)
		return
	}
	if !res.Consistent && s.metrics != nil {
		s.metrics.ReconcileDivergences.Inc()
	}
	writeJSON(w, http.StatusOK, NewReconcileResponse(res))
}

func (s *Server) processWager(w http.ResponseWriter, r *http.Request) {
	ident, ok := auth.FromContext(r.Context())
	if !ok || ident.Internal || ident.ProviderID == "" {
		writeErr(w, http.StatusForbidden, "forbidden", "provider credentials are required")
		return
	}
	key := r.Header.Get("Idempotency-Key")
	if key == "" {
		writeErr(w, http.StatusBadRequest, "missing_idempotency_key", "Idempotency-Key is required")
		return
	}
	var body ProcessWagerRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	amt, err := money.Parse(body.Money.Amount, body.Money.Currency)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_amount", err.Error())
		return
	}
	if amt.IsNegative() {
		writeErr(w, http.StatusBadRequest, "invalid_amount", "external amounts cannot be negative")
		return
	}
	kind, err := wager.ParseKind(body.Kind)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_kind", err.Error())
		return
	}
	hash, err := idemp.CanonicalHash(idemp.BusinessFields(
		body.ProviderID, body.ExternalTransactionID, body.PlayerID, body.WalletID,
		body.RoundID, body.GameID, body.Kind, amt.DecimalString(), amt.Currency(), body.ReferenceExternalID,
	))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_payload", err.Error())
		return
	}
	res, err := s.svc.ProcessWager(r.Context(), app.ProcessWagerCommand{
		ProviderID:            body.ProviderID,
		ExternalTransactionID: body.ExternalTransactionID,
		IdempotencyKey:        key,
		PlayerID:              wallet.PlayerID(body.PlayerID),
		WalletID:              wallet.ID(body.WalletID),
		RoundID:               body.RoundID,
		GameID:                body.GameID,
		Kind:                  kind,
		Amount:                amt,
		ReferenceExternalID:   body.ReferenceExternalID,
		PayloadHash:           hash,
		CorrelationID:         middleware.GetReqID(r.Context()),
		AuthenticatedProvider: ident.ProviderID,
	})
	if err != nil {
		mapDomain(w, err)
		return
	}
	if s.metrics != nil {
		s.metrics.Results.WithLabelValues(string(res.Transaction.Status()), string(res.Transaction.Kind())).Inc()
	}
	status := http.StatusOK
	if res.Transaction.Status() == wager.StatusPendingReference {
		status = http.StatusAccepted
	}
	if res.Transaction.Status() == wager.StatusRejected {
		status = http.StatusUnprocessableEntity
	}
	writeJSON(w, status, NewProcessWagerResponse(res))
}

func (s *Server) getTx(w http.ResponseWriter, r *http.Request) {
	ident, _ := auth.FromContext(r.Context())
	tx, err := s.svc.GetTransaction(r.Context(), wager.ID(chi.URLParam(r, "transactionId")), ident.ProviderID, ident.Internal)
	if err != nil {
		mapDomain(w, err)
		return
	}
	writeJSON(w, http.StatusOK, NewTransactionResponse(tx))
}

func (s *Server) getByExternal(w http.ResponseWriter, r *http.Request) {
	ident, _ := auth.FromContext(r.Context())
	providerID := chi.URLParam(r, "providerId")
	tx, err := s.svc.GetByExternal(r.Context(), providerID, chi.URLParam(r, "externalTransactionId"), ident.ProviderID)
	if err != nil {
		mapDomain(w, err)
		return
	}
	writeJSON(w, http.StatusOK, NewTransactionResponse(tx))
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, ErrorResponse{Error: code, Message: msg})
}

func mapDomain(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrWalletNotFound), errors.Is(err, domain.ErrTransactionNotFound):
		writeErr(w, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, domain.ErrWalletConflict), errors.Is(err, domain.ErrIdempotencyConflict), errors.Is(err, domain.ErrExternalIDConflict):
		writeErr(w, http.StatusConflict, "conflict", err.Error())
	case errors.Is(err, domain.ErrUnauthorizedProvider), errors.Is(err, domain.ErrUnauthorizedInternal):
		writeErr(w, http.StatusForbidden, "forbidden", err.Error())
	case errors.Is(err, domain.ErrOpeningNotAllowed), errors.Is(err, domain.ErrInvalidKind), errors.Is(err, domain.ErrInvalidAmount),
		errors.Is(err, domain.ErrInvalidIdempotencyKey), errors.Is(err, domain.ErrReferenceRequired), errors.Is(err, domain.ErrCurrencyMismatch):
		writeErr(w, http.StatusBadRequest, "invalid_request", err.Error())
	case errors.Is(err, domain.ErrInsufficientFunds), errors.Is(err, domain.ErrInsufficientFundsReversal),
		errors.Is(err, domain.ErrDuplicateReversal), errors.Is(err, domain.ErrReferenceMismatch):
		writeErr(w, http.StatusUnprocessableEntity, "rejected", err.Error())
	default:
		writeErr(w, http.StatusInternalServerError, "internal_error", "transient failure")
	}
}
