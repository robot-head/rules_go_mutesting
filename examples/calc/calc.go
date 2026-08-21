// Package calc builds on the adder package. It exists to exercise mutation
// testing of a package that depends on another Bazel target, and its tests are
// thorough enough to kill most mutants.
package calc

import "rules_go_mutesting/examples/adder"

// SumAll adds every value in xs.
func SumAll(xs []int) int {
	total := 0
	for _, x := range xs {
		total = adder.Add(total, x)
	}
	return total
}

// Clamp limits v to the inclusive range [lo, hi].
func Clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
