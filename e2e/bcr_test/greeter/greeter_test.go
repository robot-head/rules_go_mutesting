package greeter

import "testing"

// Thorough enough that the mutation gate in BUILD.bazel holds, which is the
// point of this module: a consumer's gate has to be meetable.

func TestGreet(t *testing.T) {
	cases := map[string]string{
		"":       "Hello, stranger!",
		"Ada":    "Hello, Ada!",
		"  Ada ": "Hello, Ada!",
	}
	for in, want := range cases {
		if got := Greet(in); got != want {
			t.Errorf("Greet(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRepeat(t *testing.T) {
	cases := []struct {
		s    string
		n    int
		want string
	}{
		{"hi", -1, ""},
		{"hi", 0, ""},
		{"hi", 1, "hi"},
		{"hi", 3, "hi hi hi"},
	}
	for _, tc := range cases {
		if got := Repeat(tc.s, tc.n); got != tc.want {
			t.Errorf("Repeat(%q, %d) = %q, want %q", tc.s, tc.n, got, tc.want)
		}
	}
}
