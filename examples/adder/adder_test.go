package adder

import "testing"

// These tests only check a single case each, so mutations that survive the one
// example they cover are reported as escaped.

func TestAdd(t *testing.T) {
	if got := Add(2, 3); got != 5 {
		t.Fatalf("Add(2, 3) = %d, want 5", got)
	}
}

func TestMax(t *testing.T) {
	if got := Max(4, 1); got != 4 {
		t.Fatalf("Max(4, 1) = %d, want 4", got)
	}
}

func TestScale(t *testing.T) {
	if got := Scale([]int{1, 2}, 3); len(got) != 2 {
		t.Fatalf("Scale returned %d elements, want 2", len(got))
	}
}
