package domain

import "errors"

func IsNotFound(err error) bool {
	return errors.Is(err, ErrWalletNotFound) || errors.Is(err, ErrTransactionNotFound)
}

func IsConflict(err error) bool {
	return errors.Is(err, ErrWalletConflict) ||
		errors.Is(err, ErrIdempotencyConflict) ||
		errors.Is(err, ErrExternalIDConflict) ||
		errors.Is(err, ErrPayloadConflict)
}

func IsForbidden(err error) bool {
	return errors.Is(err, ErrUnauthorizedProvider) || errors.Is(err, ErrUnauthorizedInternal)
}

func IsInvalidRequest(err error) bool {
	return errors.Is(err, ErrOpeningNotAllowed) ||
		errors.Is(err, ErrInvalidKind) ||
		errors.Is(err, ErrInvalidAmount) ||
		errors.Is(err, ErrInvalidIdempotencyKey) ||
		errors.Is(err, ErrReferenceRequired) ||
		errors.Is(err, ErrCurrencyMismatch) ||
		errors.Is(err, ErrOverflow) ||
		errors.Is(err, ErrUninitialized)
}

func IsRejected(err error) bool {
	return errors.Is(err, ErrInsufficientFunds) ||
		errors.Is(err, ErrInsufficientFundsReversal) ||
		errors.Is(err, ErrDuplicateReversal) ||
		errors.Is(err, ErrReferenceMismatch) ||
		errors.Is(err, ErrReferenceNotFound) ||
		errors.Is(err, ErrReferenceNotProcessed)
}
