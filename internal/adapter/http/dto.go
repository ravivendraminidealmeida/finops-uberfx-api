package httpapi

import (
	"time"

	"junglegaming.io/finops-api/internal/app"
	"junglegaming.io/finops-api/internal/domain/ledger"
	"junglegaming.io/finops-api/internal/domain/money"
	"junglegaming.io/finops-api/internal/domain/wager"
	"junglegaming.io/finops-api/internal/domain/wallet"
)

type MoneyPayload struct {
	Amount   string `json:"amount"`
	Currency string `json:"currency"`
}

func NewMoneyPayload(m money.Money) MoneyPayload {
	return MoneyPayload{Amount: m.DecimalString(), Currency: m.Currency()}
}

type ErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

type HealthResponse struct {
	Status string `json:"status"`
}

type OpenWalletRequest struct {
	PlayerID       string       `json:"playerId"`
	InitialBalance MoneyPayload `json:"initialBalance"`
}

type WalletResponse struct {
	ID       string       `json:"id"`
	PlayerID string       `json:"playerId"`
	Balance  MoneyPayload `json:"balance"`
	Version  int64        `json:"version"`
}

func NewWalletResponse(w *wallet.Wallet) WalletResponse {
	return WalletResponse{
		ID:       string(w.ID()),
		PlayerID: string(w.PlayerID()),
		Balance:  NewMoneyPayload(w.Balance()),
		Version:  w.Version(),
	}
}

type LedgerEntryResponse struct {
	ID            string       `json:"id"`
	WalletID      string       `json:"walletId"`
	TransactionID string       `json:"transactionId"`
	Direction     string       `json:"direction"`
	Money         MoneyPayload `json:"money"`
	BalanceBefore MoneyPayload `json:"balanceBefore"`
	BalanceAfter  MoneyPayload `json:"balanceAfter"`
	CreatedAt     string       `json:"createdAt"`
}

func NewLedgerEntryResponse(e *ledger.Entry) LedgerEntryResponse {
	return LedgerEntryResponse{
		ID:            string(e.ID()),
		WalletID:      string(e.WalletID()),
		TransactionID: e.TransactionID(),
		Direction:     string(e.Direction()),
		Money:         NewMoneyPayload(e.Amount()),
		BalanceBefore: NewMoneyPayload(e.BalanceBefore()),
		BalanceAfter:  NewMoneyPayload(e.BalanceAfter()),
		CreatedAt:     e.CreatedAt().UTC().Format(time.RFC3339Nano),
	}
}

type LedgerListResponse struct {
	Items      []LedgerEntryResponse `json:"items"`
	NextCursor string                `json:"nextCursor"`
}

type ReconcileResponse struct {
	WalletID          string       `json:"walletId"`
	StoredBalance     MoneyPayload `json:"storedBalance"`
	CalculatedBalance MoneyPayload `json:"calculatedBalance"`
	Difference        MoneyPayload `json:"difference"`
	Consistent        bool         `json:"consistent"`
	CheckedEntries    int          `json:"checkedEntries"`
}

func NewReconcileResponse(res app.ReconcileResult) ReconcileResponse {
	return ReconcileResponse{
		WalletID:          string(res.WalletID),
		StoredBalance:     NewMoneyPayload(res.StoredBalance),
		CalculatedBalance: NewMoneyPayload(res.CalculatedBalance),
		Difference:        NewMoneyPayload(res.Difference),
		Consistent:        res.Consistent,
		CheckedEntries:    res.CheckedEntries,
	}
}

type ProcessWagerRequest struct {
	ProviderID            string       `json:"providerId"`
	ExternalTransactionID string       `json:"externalTransactionId"`
	PlayerID              string       `json:"playerId"`
	WalletID              string       `json:"walletId"`
	RoundID               string       `json:"roundId"`
	GameID                string       `json:"gameId"`
	Kind                  string       `json:"kind"`
	Money                 MoneyPayload `json:"money"`
	ReferenceExternalID   string       `json:"referenceExternalTransactionId,omitempty"`
}

type ProcessWagerResponse struct {
	TransactionID    string       `json:"transactionId"`
	Status           string       `json:"status"`
	Balance          MoneyPayload `json:"balance"`
	IdempotentReplay bool         `json:"idempotentReplay"`
	FailureCode      string       `json:"failureCode,omitempty"`
}

func NewProcessWagerResponse(res app.ProcessResult) ProcessWagerResponse {
	out := ProcessWagerResponse{
		TransactionID:    string(res.Transaction.ID()),
		Status:           string(res.Transaction.Status()),
		Balance:          NewMoneyPayload(res.Balance),
		IdempotentReplay: res.IdempotentReplay,
	}
	if res.Transaction.FailureCode() != "" {
		out.FailureCode = string(res.Transaction.FailureCode())
	}
	return out
}

type TransactionResponse struct {
	TransactionID string        `json:"transactionId"`
	Status        string        `json:"status"`
	Kind          string        `json:"kind"`
	WalletID      string        `json:"walletId"`
	Money         MoneyPayload  `json:"money"`
	FailureCode   string        `json:"failureCode,omitempty"`
	Balance       *MoneyPayload `json:"balance,omitempty"`
}

func NewTransactionResponse(tx *wager.Transaction) TransactionResponse {
	out := TransactionResponse{
		TransactionID: string(tx.ID()),
		Status:        string(tx.Status()),
		Kind:          string(tx.Kind()),
		WalletID:      string(tx.WalletID()),
		Money:         NewMoneyPayload(tx.Amount()),
	}
	if tx.FailureCode() != "" {
		out.FailureCode = string(tx.FailureCode())
	}
	if tx.ResultBalanceMinor() != nil {
		bal, err := money.FromMinor(*tx.ResultBalanceMinor(), tx.Amount().Currency())
		if err == nil {
			payload := NewMoneyPayload(bal)
			out.Balance = &payload
		}
	}
	return out
}
