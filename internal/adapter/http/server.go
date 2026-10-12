package httpapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"

	"junglegaming.io/finops-api/internal/adapter/auth"
	"junglegaming.io/finops-api/internal/app"
	"junglegaming.io/finops-api/internal/domain"
	"junglegaming.io/finops-api/internal/domain/wager"
	"junglegaming.io/finops-api/internal/domain/wallet"
	"junglegaming.io/finops-api/internal/platform/obs"
)

type ReadyFunc func(ctx context.Context) error

type Server struct {
	svc     *app.Service
	auth    *auth.Verifier
	readyFn ReadyFunc
	metrics *obs.Metrics
	log     *zap.Logger
}

func NewServer(svc *app.Service, verifier *auth.Verifier, ready ReadyFunc, metrics *obs.Metrics, log *zap.Logger) *Server {
	return &Server{svc: svc, auth: verifier, readyFn: ready, metrics: metrics, log: log}
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
		r.Use(middleware.Timeout(30 * time.Second))
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
	if err := s.readyFn(r.Context()); err != nil {
		writeErr(w, http.StatusServiceUnavailable, "not_ready", err.Error())
		return
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
	if err := decodeJSON(w, r, &body); err != nil {
		return
	}
	amt, err := body.InitialBalance.Money()
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_amount", err.Error())
		return
	}
	if amt.IsNegative() {
		writeErr(w, http.StatusBadRequest, "invalid_amount", "initial balance cannot be negative")
		return
	}
	res, err := s.svc.OpenWallet(r.Context(), app.OpenWalletCommand{
		PlayerID: wallet.PlayerID(body.PlayerID),
		Initial:  amt,
	})
	if err != nil {
		mapDomain(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, NewWalletResponse(res.Wallet))
}

func (s *Server) getWallet(w http.ResponseWriter, r *http.Request) {
	if !requireInternal(w, r) {
		return
	}
	wal, err := s.svc.GetWallet(r.Context(), wallet.ID(chi.URLParam(r, "walletId")))
	if err != nil {
		mapDomain(w, err)
		return
	}
	writeJSON(w, http.StatusOK, NewWalletResponse(wal))
}

func (s *Server) listLedger(w http.ResponseWriter, r *http.Request) {
	if !requireInternal(w, r) {
		return
	}
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
	if !requireInternal(w, r) {
		return
	}
	res, err := s.svc.Reconcile(r.Context(), wallet.ID(chi.URLParam(r, "walletId")))
	if err != nil {
		mapDomain(w, err)
		return
	}
	if !res.Consistent {
		s.metrics.ReconcileDivergences.Inc()
		s.log.Warn("reconciliation divergence",
			zap.String("walletId", chi.URLParam(r, "walletId")),
			zap.String("correlationId", middleware.GetReqID(r.Context())),
		)
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
	if err := decodeJSON(w, r, &body); err != nil {
		return
	}
	amt, err := body.Money.Money()
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
		mapDomain(w, err)
		return
	}
	cmd := app.ProcessWagerCommand{
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
		AuthenticatedProvider: ident.ProviderID,
	}
	started := time.Now()
	res, err := s.svc.ProcessWager(r.Context(), cmd)
	s.metrics.ProcessingSeconds.Observe(time.Since(started).Seconds())
	if err != nil {
		if domain.IsConflict(err) {
			s.metrics.Conflicts.Inc()
		}
		s.log.Info("wager rejected at gateway",
			zap.Error(err),
			zap.String("correlationId", middleware.GetReqID(r.Context())),
			zap.String("walletId", body.WalletID),
			zap.String("providerId", ident.ProviderID),
		)
		mapDomain(w, err)
		return
	}
	s.metrics.Results.WithLabelValues(string(res.Transaction.Status()), string(res.Transaction.Kind())).Inc()
	if res.IdempotentReplay {
		s.metrics.Duplicates.Inc()
	}
	s.log.Info("wager processed",
		zap.String("correlationId", middleware.GetReqID(r.Context())),
		zap.String("transactionId", string(res.Transaction.ID())),
		zap.String("walletId", body.WalletID),
		zap.String("providerId", ident.ProviderID),
		zap.String("status", string(res.Transaction.Status())),
	)
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

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_json", err.Error())
		return err
	}
	return nil
}

func requireInternal(w http.ResponseWriter, r *http.Request) bool {
	id, ok := auth.FromContext(r.Context())
	if !ok || !id.Internal {
		writeErr(w, http.StatusForbidden, "forbidden", "internal service identity required")
		return false
	}
	return true
}

func mapDomain(w http.ResponseWriter, err error) {
	switch {
	case domain.IsNotFound(err):
		writeErr(w, http.StatusNotFound, "not_found", err.Error())
	case domain.IsConflict(err):
		writeErr(w, http.StatusConflict, "conflict", err.Error())
	case domain.IsForbidden(err):
		writeErr(w, http.StatusForbidden, "forbidden", err.Error())
	case domain.IsInvalidRequest(err):
		writeErr(w, http.StatusBadRequest, "invalid_request", err.Error())
	case domain.IsRejected(err):
		writeErr(w, http.StatusUnprocessableEntity, "rejected", err.Error())
	default:
		writeErr(w, http.StatusInternalServerError, "internal_error", "transient failure")
	}
}
