package money

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"junglegaming.io/finops-api/internal/domain"
)

const Scale = 2
const ScaleFactor int64 = 100

type Money struct {
	minor    int64
	currency string
}

func Zero(currency string) (Money, error) {
	if err := validateCurrency(currency); err != nil {
		return Money{}, err
	}
	return Money{minor: 0, currency: currency}, nil
}

func FromMinor(minor int64, currency string) (Money, error) {
	if err := validateCurrency(currency); err != nil {
		return Money{}, err
	}
	return Money{minor: minor, currency: currency}, nil
}

func Parse(amount, currency string) (Money, error) {
	if err := validateCurrency(currency); err != nil {
		return Money{}, err
	}
	minor, err := parseAmount(amount)
	if err != nil {
		return Money{}, err
	}
	return Money{minor: minor, currency: currency}, nil
}

func parseAmount(amount string) (int64, error) {
	if amount == "" {
		return 0, fmt.Errorf("%w: empty amount", domain.ErrInvalidAmount)
	}
	lower := strings.ToLower(amount)
	if strings.Contains(lower, "nan") || strings.Contains(lower, "inf") {
		return 0, fmt.Errorf("%w: non-finite amount", domain.ErrInvalidAmount)
	}
	if strings.ContainsAny(amount, "eE") {
		return 0, fmt.Errorf("%w: scientific notation", domain.ErrInvalidAmount)
	}
	negative := strings.HasPrefix(amount, "-")
	if negative {
		amount = amount[1:]
	} else if strings.HasPrefix(amount, "+") {
		return 0, fmt.Errorf("%w: explicit plus", domain.ErrInvalidAmount)
	}
	whole, frac, ok := strings.Cut(amount, ".")
	if !ok || whole == "" || len(frac) != Scale {
		return 0, fmt.Errorf("%w: expected exactly two decimal places", domain.ErrInvalidAmount)
	}
	if whole[0] == '0' && len(whole) > 1 {
		return 0, fmt.Errorf("%w: leading zeros", domain.ErrInvalidAmount)
	}
	if !allDigits(whole) || !allDigits(frac) {
		return 0, fmt.Errorf("%w: non-digit", domain.ErrInvalidAmount)
	}
	wholeVal, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: overflow", domain.ErrOverflow)
	}
	fracVal, err := strconv.ParseInt(frac, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: overflow", domain.ErrOverflow)
	}
	if wholeVal > (math.MaxInt64-fracVal)/ScaleFactor {
		return 0, fmt.Errorf("%w: overflow", domain.ErrOverflow)
	}
	minor := wholeVal*ScaleFactor + fracVal
	if !negative {
		return minor, nil
	}
	neg, err := negateInt64(minor)
	if err != nil {
		return 0, err
	}
	return neg, nil
}

func allDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func negateInt64(v int64) (int64, error) {
	if v == math.MinInt64 {
		return 0, domain.ErrOverflow
	}
	return -v, nil
}

func validateCurrency(currency string) error {
	if len(currency) != 3 {
		return fmt.Errorf("%w: %q", domain.ErrInvalidAmount, currency)
	}
	for _, r := range currency {
		if r < 'A' || r > 'Z' {
			return fmt.Errorf("%w: %q", domain.ErrInvalidAmount, currency)
		}
	}
	return nil
}

func (m Money) Minor() int64      { return m.minor }
func (m Money) Currency() string  { return m.currency }
func (m Money) IsZero() bool      { return m.minor == 0 }
func (m Money) IsNegative() bool  { return m.minor < 0 }
func (m Money) IsPositive() bool  { return m.minor > 0 }
func (m Money) Initialized() bool { return m.currency != "" }

func (m Money) requireSameCurrency(other Money) error {
	if !m.Initialized() || !other.Initialized() {
		return domain.ErrUninitialized
	}
	if m.currency != other.currency {
		return fmt.Errorf("%w: %s vs %s", domain.ErrCurrencyMismatch, m.currency, other.currency)
	}
	return nil
}

func (m Money) Add(other Money) (Money, error) {
	if err := m.requireSameCurrency(other); err != nil {
		return Money{}, err
	}
	sum, err := addInt64(m.minor, other.minor)
	if err != nil {
		return Money{}, err
	}
	return Money{minor: sum, currency: m.currency}, nil
}

func (m Money) Sub(other Money) (Money, error) {
	if err := m.requireSameCurrency(other); err != nil {
		return Money{}, err
	}
	neg, err := other.Negate()
	if err != nil {
		return Money{}, err
	}
	return m.Add(neg)
}

func (m Money) Negate() (Money, error) {
	if !m.Initialized() {
		return Money{}, domain.ErrUninitialized
	}
	if m.minor == math.MinInt64 {
		return Money{}, domain.ErrOverflow
	}
	return Money{minor: -m.minor, currency: m.currency}, nil
}

func (m Money) Cmp(other Money) (int, error) {
	if err := m.requireSameCurrency(other); err != nil {
		return 0, err
	}
	switch {
	case m.minor < other.minor:
		return -1, nil
	case m.minor > other.minor:
		return 1, nil
	default:
		return 0, nil
	}
}

func (m Money) Equal(other Money) (bool, error) {
	cmp, err := m.Cmp(other)
	return cmp == 0, err
}

func (m Money) Less(other Money) (bool, error) {
	cmp, err := m.Cmp(other)
	return cmp < 0, err
}

func addInt64(a, b int64) (int64, error) {
	if b > 0 && a > math.MaxInt64-b {
		return 0, domain.ErrOverflow
	}
	if b < 0 && a < math.MinInt64-b {
		return 0, domain.ErrOverflow
	}
	return a + b, nil
}

func (m Money) DecimalString() string {
	sign := ""
	abs := uint64(m.minor)
	if m.minor < 0 {
		sign = "-"
		abs = uint64(^m.minor) + 1
	}
	return fmt.Sprintf("%s%d.%02d", sign, abs/uint64(ScaleFactor), abs%uint64(ScaleFactor))
}
