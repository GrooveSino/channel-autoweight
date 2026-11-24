package firstpass

import (
	"fmt"
	"math"
	"strconv"
)

// Milli is fixed-point strength (or signed difference), never a routing probability.
type Milli int

func (v Milli) String() string               { return strconv.FormatFloat(float64(v)/1000, 'f', 3, 64) }
func (v Milli) MarshalJSON() ([]byte, error) { return []byte(v.String()), nil }
func CheckedRatio(q float64) (Milli, error) {
	if math.IsNaN(q) || math.IsInf(q, 0) {
		return 0, fmt.Errorf("non-finite strength")
	}
	return Milli(math.Round(1000 * min(1, max(0, q)))), nil
}
func Ratio(q float64) Milli {
	v, err := CheckedRatio(q)
	if err != nil {
		panic(err)
	}
	return v
}
func (v Milli) Weight(scale int) int { return int((int64(v)*int64(scale) + 500) / 1000) }
func ptr(v Milli) *Milli             { return &v }
