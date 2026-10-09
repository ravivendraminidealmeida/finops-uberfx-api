package wager

type FailureCode string

const (
	FailureInsufficientFunds         FailureCode = "INSUFFICIENT_FUNDS"
	FailureInsufficientFundsReversal FailureCode = "INSUFFICIENT_FUNDS_REVERSAL"
	FailureReferenceNotFound         FailureCode = "REFERENCE_NOT_FOUND"
	FailureReferenceNotProcessed     FailureCode = "REFERENCE_NOT_PROCESSED"
	FailureReferenceMismatch         FailureCode = "REFERENCE_MISMATCH"
	FailureDuplicateReversal         FailureCode = "DUPLICATE_REVERSAL"
	FailureInvalidAmount             FailureCode = "INVALID_AMOUNT"
	FailureCurrencyMismatch          FailureCode = "CURRENCY_MISMATCH"
	FailureOpeningNotAllowed         FailureCode = "OPENING_NOT_ALLOWED"
	FailureWalletNotFound            FailureCode = "WALLET_NOT_FOUND"
	FailureIdempotencyConflict       FailureCode = "IDEMPOTENCY_CONFLICT"
	FailureExternalIDConflict        FailureCode = "EXTERNAL_ID_CONFLICT"
	FailureInvalidKind               FailureCode = "INVALID_KIND"
	FailurePermanentInfrastructure   FailureCode = "PERMANENT_INFRASTRUCTURE"
)
