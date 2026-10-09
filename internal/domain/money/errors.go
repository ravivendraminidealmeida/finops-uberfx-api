package money

import "errors"

var (
	ErrInvalidAmount    = errors.New("invalid monetary amount")
	ErrInvalidCurrency  = errors.New("invalid currency")
	ErrCurrencyMismatch = errors.New("currency mismatch")
	ErrOverflow         = errors.New("monetary overflow")
	ErrUninitialized    = errors.New("uninitialized money")
)
