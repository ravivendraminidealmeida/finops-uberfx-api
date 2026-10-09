package wager

import (
	"time"

	"junglegaming.io/finops-api/internal/domain/money"
	"junglegaming.io/finops-api/internal/domain/wallet"
)

func MustRehydrate(
	id, origin, kind, status, walletID, playerID string,
	amt money.Money,
	provider, extID, idem, hash, roundID, gameID, refExt, refInt, fail string,
	resultMinor *int64, attempts int, next *time.Time, created, updated time.Time, processed *time.Time,
) Transaction {
	var nextCopy *time.Time
	if next != nil {
		t := next.UTC()
		nextCopy = &t
	}
	var procCopy *time.Time
	if processed != nil {
		t := processed.UTC()
		procCopy = &t
	}
	return Transaction{
		id:                    ID(id),
		origin:                Origin(origin),
		kind:                  Kind(kind),
		status:                Status(status),
		walletID:              wallet.ID(walletID),
		playerID:              wallet.PlayerID(playerID),
		amount:                amt,
		providerID:            provider,
		externalTransactionID: extID,
		idempotencyKey:        idem,
		payloadHash:           hash,
		roundID:               roundID,
		gameID:                gameID,
		referenceExternalID:   refExt,
		referencedInternalID:  ID(refInt),
		failureCode:           FailureCode(fail),
		resultBalanceMinor:    resultMinor,
		attemptCount:          attempts,
		nextAttemptAt:         nextCopy,
		createdAt:             created.UTC(),
		updatedAt:             updated.UTC(),
		processedAt:           procCopy,
	}
}
