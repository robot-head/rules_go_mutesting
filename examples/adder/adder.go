// Package adder provides small arithmetic helpers used to exercise the
// mutation testing aspect. Its tests are deliberately weak so that some
// mutants survive.
package adder

// Add returns the sum of a and b.
func Add(a, b int) int {
	return a + b
}

// Max returns the larger of a and b.
func Max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// Scale multiplies every element of xs by factor, in place.
func Scale(xs []int, factor int) []int {
	for i := range xs {
		xs[i] = xs[i] * factor
	}
	return xs
}
