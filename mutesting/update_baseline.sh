#!/usr/bin/env bash
#
# Records the currently surviving mutants of a package as a baseline file, so
# that later runs can tolerate them while still failing on new ones.
#
#   bazel run @rules_go_mutesting//mutesting:update_baseline -- ./path/to/pkg
#
# This writes into the source tree and depends on the host Go toolchain, so it
# is deliberately a run target rather than a cached build action.

set -euo pipefail

if [[ -z "${BUILD_WORKSPACE_DIRECTORY:-}" ]]; then
	echo "mutesting: run this through bazel run, not directly" >&2
	exit 2
fi

if [[ $# -eq 0 ]]; then
	cat >&2 <<'EOF'
Usage: bazel run @rules_go_mutesting//mutesting:update_baseline -- <package> [tool flags]

Writes go-mutesting-baseline.json for the given package, relative to the
workspace root. Point the aspect at it with:
  --@rules_go_mutesting//mutesting:baseline=//path/to:go-mutesting-baseline.json
EOF
	exit 2
fi

TOOL="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/../mutesting/tool"
if [[ ! -x "$TOOL" ]]; then
	# Resolve through runfiles when the layout differs.
	TOOL="$(find "${RUNFILES_DIR:-$PWD}" -name 'go-mutesting' -type f -perm -u+x 2>/dev/null | head -1)"
fi
[[ -x "$TOOL" ]] || {
	echo "mutesting: cannot locate the go-mutesting binary in runfiles" >&2
	exit 1
}

cd "$BUILD_WORKSPACE_DIRECTORY"
exec "$TOOL" --update-baseline "$@"
