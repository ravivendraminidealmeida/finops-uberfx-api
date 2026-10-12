package wire

import "junglegaming.io/finops-api/internal/domain/money"

type Money struct {
	Amount   string `json:"amount"`
	Currency string `json:"currency"`
}

func FromMoney(m money.Money) Money {
	return Money{Amount: m.DecimalString(), Currency: m.Currency()}
}

func (p Money) Money() (money.Money, error) {
	return money.Parse(p.Amount, p.Currency)
}
