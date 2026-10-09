package money

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"unicode"
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
		return 0, fmt.Errorf("%w: empty amount", ErrInvalidAmount)
	}
	lower := strings.ToLower(amount)
	if strings.Contains(lower, "nan") || strings.Contains(lower, "inf") {
		return 0, fmt.Errorf("%w: non-finite amount", ErrInvalidAmount)
	}
	if strings.ContainsAny(amount, "eE") {
		return 0, fmt.Errorf("%w: scientific notation", ErrInvalidAmount)
	}
	negative := false
	if strings.HasPrefix(amount, "-") {
		negative = true
		amount = amount[1:]
	} else if strings.HasPrefix(amount, "+") {
		return 0, fmt.Errorf("%w: explicit plus", ErrInvalidAmount)
	}
	parts := strings.Split(amount, ".")
	if len(parts) != 2 {
		return 0, fmt.Errorf("%w: expected exactly two decimal places", ErrInvalidAmount)
	}
	whole, frac := parts[0], parts[1]
	if whole == "" || frac == "" || len(frac) != Scale {
		return 0, fmt.Errorf("%w: expected exactly two decimal places", ErrInvalidAmount)
	}
	if whole[0] == '0' && len(whole) > 1 {
		return 0, fmt.Errorf("%w: leading zeros", ErrInvalidAmount)
	}
	for _, r := range whole + frac {
		if !unicode.IsDigit(r) {
			return 0, fmt.Errorf("%w: non-digit", ErrInvalidAmount)
		}
	}
	var wholeVal int64
	for _, r := range whole {
		digit := int64(r - '0')
		if wholeVal > (math.MaxInt64-digit)/10 {
			return 0, fmt.Errorf("%w: overflow", ErrOverflow)
		}
		wholeVal = wholeVal*10 + digit
	}
	var fracVal int64
	for _, r := range frac {
		fracVal = fracVal*10 + int64(r-'0')
	}
	if wholeVal > (math.MaxInt64-fracVal)/ScaleFactor {
		return 0, fmt.Errorf("%w: overflow", ErrOverflow)
	}
	minor := wholeVal*ScaleFactor + fracVal
	if negative {
		if minor == 0 {
			return 0, nil
		}
		if minor > 0 && -minor > 0 {
			return 0, fmt.Errorf("%w: overflow", ErrOverflow)
		}
		return -minor, nil
	}
	return minor, nil
}

func validateCurrency(currency string) error {
	if len(currency) != 3 {
		return fmt.Errorf("%w: %q", ErrInvalidCurrency, currency)
	}
	for _, r := range currency {
		if r < 'A' || r > 'Z' {
			return fmt.Errorf("%w: %q", ErrInvalidCurrency, currency)
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
		return ErrUninitialized
	}
	if m.currency != other.currency {
		return fmt.Errorf("%w: %s vs %s", ErrCurrencyMismatch, m.currency, other.currency)
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
		return Money{}, ErrUninitialized
	}
	if m.minor == math.MinInt64 {
		return Money{}, ErrOverflow
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

func addInt64(a, b int64) (int64, error) {
	if b > 0 && a > math.MaxInt64-b {
		return 0, ErrOverflow
	}
	if b < 0 && a < math.MinInt64-b {
		return 0, ErrOverflow
	}
	return a + b, nil
}

func (m Money) DecimalString() string {
	sign := ""
	v := m.minor
	if v < 0 {
		sign = "-"
		v = -v
	}
	return fmt.Sprintf("%s%d.%02d", sign, v/ScaleFactor, v%ScaleFactor)
}

func (m Money) MarshalJSON() ([]byte, error) {
	if !m.Initialized() {
		return nil, ErrUninitialized
	}
	return json.Marshal(struct {
		Amount   string `json:"amount"`
		Currency string `json:"currency"`
	}{Amount: m.DecimalString(), Currency: m.currency})
}

func (m *Money) UnmarshalJSON(data []byte) error {
	var raw struct {
		Amount   string `json:"amount"`
		Currency string `json:"currency"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	parsed, err := Parse(raw.Amount, raw.Currency)
	if err != nil {
		return err
	}
	*m = parsed
	return nil
}
