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
	FailurePermanentInfrastructure   FailureCode = "PERMANENT_INFRASTRUCTURE"
)
