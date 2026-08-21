package calc

import "testing"

func TestSumAll(t *testing.T) {
	cases := []struct {
		name string
		in   []int
		want int
	}{
		{"empty", nil, 0},
		{"single", []int{7}, 7},
		{"several", []int{1, 2, 3, 4}, 10},
		{"negatives", []int{5, -2, -3}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SumAll(tc.in); got != tc.want {
				t.Fatalf("SumAll(%v) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

func TestClamp(t *testing.T) {
	cases := []struct {
		name            string
		v, lo, hi, want int
	}{
		{"below", -5, 0, 10, 0},
		{"above", 42, 0, 10, 10},
		{"inside", 5, 0, 10, 5},
		{"at low bound", 0, 0, 10, 0},
		{"at high bound", 10, 0, 10, 10},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Clamp(tc.v, tc.lo, tc.hi); got != tc.want {
				t.Fatalf("Clamp(%d, %d, %d) = %d, want %d", tc.v, tc.lo, tc.hi, got, tc.want)
			}
		})
	}
}
