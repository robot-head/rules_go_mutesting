// Package greeter is the code under mutation in the consumer test module.
package greeter

import "strings"

// Greet builds a greeting for name, falling back to a generic one.
func Greet(name string) string {
	if name == "" {
		return "Hello, stranger!"
	}
	return "Hello, " + strings.TrimSpace(name) + "!"
}

// Repeat returns s joined to itself n times, separated by spaces.
func Repeat(s string, n int) string {
	if n <= 0 {
		return ""
	}
	parts := make([]string, 0, n)
	for i := 0; i < n; i++ {
		parts = append(parts, s)
	}
	return strings.Join(parts, " ")
}
