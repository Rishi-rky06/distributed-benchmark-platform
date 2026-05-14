package botfleet

import (
	"math"
	"math/rand"
	"time"

	"github.com/google/uuid"
)

// Order types
type OrderType string
type Side string

const (
	OrderTypeLimit  OrderType = "limit"
	OrderTypeMarket OrderType = "market"
	OrderTypeCancel OrderType = "cancel"

	SideBuy  Side = "buy"
	SideSell Side = "sell"
)

// Order represents a trading order sent to the contestant's engine.
type Order struct {
	ID        string    `json:"id"`
	Type      OrderType `json:"type"`
	Side      Side      `json:"side"`
	Symbol    string    `json:"symbol"`
	Price     float64   `json:"price,omitempty"`
	Quantity  int       `json:"quantity"`
	Timestamp int64     `json:"timestamp"`
}

// OrderGenerator produces stochastic trading orders.
type OrderGenerator struct {
	rng        *rand.Rand
	symbols    []string
	// Ornstein-Uhlenbeck price parameters
	prices     map[string]float64 // current price per symbol
	basePrices map[string]float64 // mean-reversion targets per symbol
	theta      float64            // mean reversion speed
	sigma      float64            // volatility
}

func NewOrderGenerator() *OrderGenerator {
	symbols := []string{"AAPL", "MSFT", "GOOG", "AMZN"}
	prices := make(map[string]float64, len(symbols))
	bases := map[string]float64{"AAPL": 175.0, "MSFT": 420.0, "GOOG": 165.0, "AMZN": 185.0}
	for _, s := range symbols {
		prices[s] = bases[s]
	}

	return &OrderGenerator{
		rng:        rand.New(rand.NewSource(time.Now().UnixNano())),
		symbols:    symbols,
		prices:     prices,
		basePrices: bases,
		theta:      0.15, // reversion speed
		sigma:      0.5,  // volatility
	}
}

// Generate produces a random order following the configured distribution.
// Default mix: 60% limit, 30% market, 10% cancel.
func (g *OrderGenerator) Generate() Order {
	symbol := g.symbols[g.rng.Intn(len(g.symbols))]
	g.updatePrice(symbol)

	r := g.rng.Float64()
	var orderType OrderType
	switch {
	case r < 0.6:
		orderType = OrderTypeLimit
	case r < 0.9:
		orderType = OrderTypeMarket
	default:
		orderType = OrderTypeCancel
	}

	side := SideBuy
	if g.rng.Float64() < 0.5 {
		side = SideSell
	}

	price := g.prices[symbol]
	// Add spread: buyers bid lower, sellers ask higher
	if side == SideBuy {
		price *= (1 - g.rng.Float64()*0.002) // 0-0.2% below
	} else {
		price *= (1 + g.rng.Float64()*0.002) // 0-0.2% above
	}
	price = math.Round(price*100) / 100 // round to cents

	// Log-normal quantity distribution, centered around 100
	qty := int(math.Exp(g.rng.NormFloat64()*0.5+math.Log(100)))
	if qty < 1 {
		qty = 1
	}
	if qty > 10000 {
		qty = 10000
	}

	return Order{
		ID:        uuid.NewString()[:8],
		Type:      orderType,
		Side:      side,
		Symbol:    symbol,
		Price:     price,
		Quantity:  qty,
		Timestamp: time.Now().UnixNano(),
	}
}

// updatePrice applies an Ornstein-Uhlenbeck step to the symbol's price.
func (g *OrderGenerator) updatePrice(symbol string) {
	current := g.prices[symbol]
	base := g.basePrices[symbol]
	dt := 1.0 / 1000.0 // ~1ms step
	drift := -g.theta * (math.Log(current) - math.Log(base)) * dt
	diffusion := g.sigma * math.Sqrt(dt) * g.rng.NormFloat64()
	g.prices[symbol] = current * math.Exp(drift+diffusion)
}
