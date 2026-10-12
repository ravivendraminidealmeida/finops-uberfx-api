package httpapi

import (
	"time"

	"junglegaming.io/finops-api/internal/adapter/wire"
	"junglegaming.io/finops-api/internal/app"
	"junglegaming.io/finops-api/internal/domain/ledger"
	"junglegaming.io/finops-api/internal/domain/wager"
	"junglegaming.io/finops-api/internal/domain/wallet"
)

type ErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

type HealthResponse struct {
	Status string `json:"status"`
}

type OpenWalletRequest struct {
	PlayerID       string     `json:"playerId"`
	InitialBalance wire.Money `json:"initialBalance"`
}

type WalletResponse struct {
	ID       string     `json:"id"`
	PlayerID string     `json:"playerId"`
	Balance  wire.Money `json:"balance"`
	Version  int64      `json:"version"`
}

func NewWalletResponse(w *wallet.Wallet) WalletResponse {
	return WalletResponse{
		ID:       string(w.ID()),
		PlayerID: string(w.PlayerID()),
		Balance:  wire.FromMoney(w.Balance()),
		Version:  w.Version(),
	}
}

type LedgerEntryResponse struct {
	ID            string     `json:"id"`
	WalletID      string     `json:"walletId"`
	TransactionID string     `json:"transactionId"`
	Direction     string     `json:"direction"`
	Money         wire.Money `json:"money"`
	BalanceBefore wire.Money `json:"balanceBefore"`
	BalanceAfter  wire.Money `json:"balanceAfter"`
	CreatedAt     string     `json:"createdAt"`
}

func NewLedgerEntryResponse(e *ledger.Entry) LedgerEntryResponse {
	return LedgerEntryResponse{
		ID:            string(e.ID()),
		WalletID:      string(e.WalletID()),
		TransactionID: e.TransactionID(),
		Direction:     string(e.Direction()),
		Money:         wire.FromMoney(e.Amount()),
		BalanceBefore: wire.FromMoney(e.BalanceBefore()),
		BalanceAfter:  wire.FromMoney(e.BalanceAfter()),
		CreatedAt:     e.CreatedAt().UTC().Format(time.RFC3339Nano),
	}
}

type LedgerListResponse struct {
	Items      []LedgerEntryResponse `json:"items"`
	NextCursor string                `json:"nextCursor"`
}

type ReconcileResponse struct {
	WalletID          string     `json:"walletId"`
	StoredBalance     wire.Money `json:"storedBalance"`
	CalculatedBalance wire.Money `json:"calculatedBalance"`
	Difference        wire.Money `json:"difference"`
	Consistent        bool       `json:"consistent"`
	CheckedEntries    int        `json:"checkedEntries"`
}

func NewReconcileResponse(res app.ReconcileResult) ReconcileResponse {
	return ReconcileResponse{
		WalletID:          string(res.WalletID),
		StoredBalance:     wire.FromMoney(res.StoredBalance),
		CalculatedBalance: wire.FromMoney(res.CalculatedBalance),
		Difference:        wire.FromMoney(res.Difference),
		Consistent:        res.Consistent,
		CheckedEntries:    res.CheckedEntries,
	}
}

type ProcessWagerRequest struct {
	ProviderID            string     `json:"providerId"`
	ExternalTransactionID string     `json:"externalTransactionId"`
	PlayerID              string     `json:"playerId"`
	WalletID              string     `json:"walletId"`
	RoundID               string     `json:"roundId"`
	GameID                string     `json:"gameId"`
	Kind                  string     `json:"kind"`
	Money                 wire.Money `json:"money"`
	ReferenceExternalID   string     `json:"referenceExternalTransactionId,omitempty"`
}

type ProcessWagerResponse struct {
	TransactionID    string     `json:"transactionId"`
	Status           string     `json:"status"`
	Balance          wire.Money `json:"balance"`
	IdempotentReplay bool       `json:"idempotentReplay"`
	FailureCode      string     `json:"failureCode,omitempty"`
}

func NewProcessWagerResponse(res app.ProcessResult) ProcessWagerResponse {
	out := ProcessWagerResponse{
		TransactionID:    string(res.Transaction.ID()),
		Status:           string(res.Transaction.Status()),
		Balance:          wire.FromMoney(res.Balance),
		IdempotentReplay: res.IdempotentReplay,
	}
	if res.Transaction.FailureCode() != "" {
		out.FailureCode = string(res.Transaction.FailureCode())
	}
	return out
}

type TransactionResponse struct {
	TransactionID string      `json:"transactionId"`
	Status        string      `json:"status"`
	Kind          string      `json:"kind"`
	WalletID      string      `json:"walletId"`
	Money         wire.Money  `json:"money"`
	FailureCode   string      `json:"failureCode,omitempty"`
	Balance       *wire.Money `json:"balance,omitempty"`
}

func NewTransactionResponse(tx *wager.Transaction) TransactionResponse {
	out := TransactionResponse{
		TransactionID: string(tx.ID()),
		Status:        string(tx.Status()),
		Kind:          string(tx.Kind()),
		WalletID:      string(tx.WalletID()),
		Money:         wire.FromMoney(tx.Amount()),
	}
	if tx.FailureCode() != "" {
		out.FailureCode = string(tx.FailureCode())
	}
	if bal, ok := tx.ResultBalance(); ok {
		payload := wire.FromMoney(bal)
		out.Balance = &payload
	}
	return out
}
