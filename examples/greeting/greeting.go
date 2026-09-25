// Package greeting builds greetings.
package greeting

// Greet returns a greeting for name, or a generic one when name is empty.
func Greet(name string) string {
	if name == "" {
		return "Hello!"
	}
	return "Hello, " + name + "!"
}
