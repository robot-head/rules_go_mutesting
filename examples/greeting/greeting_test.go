package greeting

import (
	_ "embed"
	"strings"
	"testing"
)

//go:embed testdata/greetings.txt
var golden string

func TestGreet(t *testing.T) {
	want := strings.Split(strings.TrimSpace(golden), "\n")
	got := []string{Greet(""), Greet("Ada")}
	if len(got) != len(want) {
		t.Fatalf("got %d greetings, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("greeting %d = %q, want %q", i, got[i], want[i])
		}
	}
}
