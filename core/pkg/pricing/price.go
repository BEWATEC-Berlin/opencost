package pricing

import (
	"errors"

	"github.com/opencost/opencost/core/pkg/unit"
)

var NotFound = errors.New("Not found")

type Price struct {
	Resource Resource      `json:"resource" yaml:"resource"`
	Unit     unit.Unit     `json:"unit" yaml:"unit"`
	Currency unit.Currency `json:"currency" yaml:"currency"`
	Price    float64       `json:"price" yaml:"price"`
}

type Prices map[unit.Currency][]Price

func (p Prices) GetPrices() []Price {
	prices := make([]Price, 0, len(p))

	for _, price := range p {
		prices = append(prices, price...)
	}

	return prices
}

func (p Prices) GetPricesInCurrency(currency unit.Currency) ([]Price, error) {
	result := []Price{}

	for curr, prices := range p {
		if curr == currency {
			result = append(result, prices...)
		}
	}

	if len(result) == 0 {
		return nil, NotFound
	}

	return result, nil
}

func (p Prices) GetPricesInCurrencyWithDefault(currency, defaultCurrency unit.Currency) ([]Price, error) {
	prices, err := p.GetPricesInCurrency(currency)
	if len(prices) > 0 && err == nil {
		return prices, nil
	}

	return p.GetPricesInCurrency(defaultCurrency)
}
