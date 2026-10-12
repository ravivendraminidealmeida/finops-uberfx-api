package wager

import (
	"time"

	"junglegaming.io/finops-api/internal/domain"
	"junglegaming.io/finops-api/internal/domain/money"
	"junglegaming.io/finops-api/internal/domain/wallet"
)

// Snapshot is the persistence projection. Optional SQL columns stay as pointers here, not on the aggregate.
type Snapshot struct {
	ID, Origin, Kind, Status, WalletID, PlayerID                    string
	Amount                                                          money.Money
	ProviderID, ExternalID, IdempotencyKey, PayloadHash             string
	RoundID, GameID, ReferenceExternalID, ReferencedID, FailureCode string
	ResultMinor                                                     *int64
	AttemptCount                                                    int
	NextAttempt, Processed                                          *time.Time
	CreatedAt, UpdatedAt                                            time.Time
}

func Rehydrate(s Snapshot) (*Transaction, error) {
	t := Transaction{
		id:                    ID(s.ID),
		origin:                Origin(s.Origin),
		kind:                  Kind(s.Kind),
		status:                Status(s.Status),
		walletID:              wallet.ID(s.WalletID),
		playerID:              wallet.PlayerID(s.PlayerID),
		amount:                s.Amount,
		providerID:            s.ProviderID,
		externalTransactionID: s.ExternalID,
		idempotencyKey:        s.IdempotencyKey,
		payloadHash:           s.PayloadHash,
		roundID:               s.RoundID,
		gameID:                s.GameID,
		referenceExternalID:   s.ReferenceExternalID,
		referencedInternalID:  ID(s.ReferencedID),
		failureCode:           FailureCode(s.FailureCode),
		attemptCount:          s.AttemptCount,
		createdAt:             s.CreatedAt.UTC(),
		updatedAt:             s.UpdatedAt.UTC(),
	}
	if s.ResultMinor != nil {
		bal, err := money.FromMinor(*s.ResultMinor, s.Amount.Currency())
		if err != nil {
			return nil, err
		}
		t.resultBalance = bal
	}
	if s.NextAttempt != nil {
		t.nextAttemptAt = s.NextAttempt.UTC()
	}
	if s.Processed != nil {
		t.processedAt = s.Processed.UTC()
	}
	if t.id == "" || t.walletID == "" || t.playerID == "" || !t.amount.Initialized() {
		return nil, domain.ErrUninitialized
	}
	return &t, nil
}
