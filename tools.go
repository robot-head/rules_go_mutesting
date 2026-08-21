//go:build tools

// Package tools pins build-time dependencies that no library code imports.
//
// The go-mutesting command is built by Bazel as a hermetic tool binary; listing
// it here keeps `go mod tidy` from dropping it from go.mod, which is what feeds
// gazelle's go_deps extension in MODULE.bazel.
package tools

import (
	_ "github.com/jonbaldie/go-mutesting/v2/cmd/go-mutesting"
)
