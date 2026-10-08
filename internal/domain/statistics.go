package domain

import (
	"math"
	"strconv"
)

type Statistic struct {
	Key, Label, Value string
	RawValue          float64
}
type Calculator interface{ Calculate(Match) (Statistic, bool) }
type Govnoedstvo struct{}

func (Govnoedstvo) Calculate(m Match) (Statistic, bool) {
	impact := float64(m.Kills+m.Assists) - float64(m.Deaths)*1.25
	normalized := min(max(impact/15, 0), 1)
	percent := int(math.RoundToEven((1 - normalized) * 100))
	return Statistic{Key: "govnoedstvo", Label: "Govnoedstvo", Value: strconv.Itoa(percent) + "%", RawValue: float64(percent)}, true
}
func CalculateStatistics(m Match, calculators ...Calculator) []Statistic {
	if len(calculators) == 0 {
		calculators = []Calculator{Govnoedstvo{}}
	}
	var out []Statistic
	for _, c := range calculators {
		if stat, ok := c.Calculate(m); ok {
			out = append(out, stat)
		}
	}
	return out
}
