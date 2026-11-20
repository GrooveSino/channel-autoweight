// Package model holds the plain data structs exchanged between layers, plus
// small helpers that reproduce Python numeric semantics exactly (round, pct).
package model

import (
	"fmt"
	"math"
	"sort"
	"strconv"
)

// Round reproduces Python's round(x, n): correctly rounded, ties to even on the
// exact binary value (strconv uses the same rule).
func Round(x float64, n int) float64 {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return x
	}
	v, _ := strconv.ParseFloat(strconv.FormatFloat(x, 'f', n, 64), 64)
	return v
}

// RoundInt reproduces Python's round(x) (no ndigits): banker's rounding.
func RoundInt(x float64) int { return int(math.RoundToEven(x)) }

// Pct reproduces features.pct: s[min(len-1, int(q*(len-1)+0.5))] on sorted copy.
func Pct(xs []float64, q float64) *float64 {
	if len(xs) == 0 {
		return nil
	}
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	k := int(q*float64(len(s)-1) + 0.5)
	if k > len(s)-1 {
		k = len(s) - 1
	}
	v := s[k]
	return &v
}

// Median of a sorted-or-not slice using Python's "middle or mean of two" rule.
func Median(xs []float64) float64 {
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}

func F(v float64) *float64 { return &v }
func I(v int) *int         { return &v }
func B(v bool) *bool       { return &v }

// Or returns *p or d when p is nil (Python "x or d" for values where 0 is also falsy
// must use OrZ instead).
func Or(p *float64, d float64) float64 {
	if p == nil {
		return d
	}
	return *p
}

// OrZ mimics Python "(x or d)": nil and 0 both map to d.
func OrZ(p *float64, d float64) float64 {
	if p == nil || *p == 0 {
		return d
	}
	return *p
}

func OrI(p *int, d int) int {
	if p == nil {
		return d
	}
	return *p
}

func Pow(a, b float64) float64 { return math.Pow(a, b) }

func fmtSscan(s string, i *int) (int, error) { return fmt.Sscan(s, i) }
