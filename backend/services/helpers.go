package services

import (
	"encoding/json"
	"math"
)

func logBase10(v float64) float64 {
	if v <= 0 {
		return 0
	}
	return math.Log10(v)
}

func clamp(v, min, max float64) float64 {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

func jsonMarshal(v any) ([]byte, error) {
	return json.Marshal(v)
}
