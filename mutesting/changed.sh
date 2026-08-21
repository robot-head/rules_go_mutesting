#!/usr/bin/env bash
#
# Runs mutation testing only on lines changed since a git base ref.
#
#   bazel run @rules_go_mutesting//mutesting:changed -- --base origin/main
#
# The diff is computed here, in the source tree, because a Bazel action has no
# git repository to look at. What crosses into the build is a normalized list
# of changed line ranges, injected as an external repository so that the aspect
# can take it as an ordinary action input.

set -euo pipefail

usage() {
	cat >&2 <<'EOF'
Usage: bazel run @rules_go_mutesting//mutesting:changed -- --base <ref> [options] [-- <bazel flags>]

Required:
  --base <ref>      Git ref to compare against. Mutation testing covers lines
                    changed between the merge base of <ref> and the working
                    tree, so uncommitted edits are included.

Options:
  --scope <pattern> Target pattern to search for owners (default //...).
  --dry-run         Print the diff summary, selected targets and the bazel
                    command without building.
  -h, --help        Show this message.

Anything after a bare -- is passed to the underlying bazel build, for example
  --base origin/main -- --@rules_go_mutesting//mutesting:workers=4
EOF
}

die() {
	echo "mutesting: $*" >&2
	exit 2
}

BASE=""
SCOPE="//..."
DRY_RUN=0
PASSTHROUGH=()

while [[ $# -gt 0 ]]; do
	case "$1" in
	--base)
		[[ $# -ge 2 ]] || die "--base needs a value"
		BASE="$2"
		shift 2
		;;
	--base=*)
		BASE="${1#*=}"
		shift
		;;
	--scope)
		[[ $# -ge 2 ]] || die "--scope needs a value"
		SCOPE="$2"
		shift 2
		;;
	--scope=*)
		SCOPE="${1#*=}"
		shift
		;;
	--dry-run)
		DRY_RUN=1
		shift
		;;
	-h | --help)
		usage
		exit 0
		;;
	--)
		shift
		PASSTHROUGH=("$@")
		break
		;;
	*)
		die "unknown argument: $1 (use -- to pass flags to bazel)"
		;;
	esac
done

[[ -n "${BUILD_WORKSPACE_DIRECTORY:-}" ]] ||
	die "run this through bazel run, not directly"
[[ -n "$BASE" ]] || {
	usage
	die "--base is required; there is no default diff base"
}
command -v git >/dev/null 2>&1 || die "git is required to compute the diff"

cd "$BUILD_WORKSPACE_DIRECTORY"

git rev-parse --is-inside-work-tree >/dev/null 2>&1 ||
	die "$BUILD_WORKSPACE_DIRECTORY is not inside a git repository"

BASE_SHA="$(git rev-parse --verify --quiet "${BASE}^{commit}" || true)"
[[ -n "$BASE_SHA" ]] ||
	die "cannot resolve --base $BASE; fetch it first, e.g. git fetch origin main"

MERGE_BASE="$(git merge-base "$BASE_SHA" HEAD 2>/dev/null || true)"
[[ -n "$MERGE_BASE" ]] ||
	die "no common ancestor between HEAD and $BASE; a shallow clone needs more history"

# Bazel workspaces do not have to sit at the root of the git repository, so
# rewrite paths to be workspace relative and drop anything outside it.
PREFIX="$(git rev-parse --show-prefix)"

DIFF_RAW="$(git diff --merge-base "$BASE_SHA" -U0 -M --diff-filter=d -- '*.go' || true)"

# Turn the unified diff into "path start end" triples covering the new side of
# each hunk. A hunk that only deletes lines is recorded against the surviving
# line next to it, so mutants there stay in scope.
RANGES="$(awk -v prefix="$PREFIX" '
	/^\+\+\+ /  {
		path = substr($0, 7)          # strip "+++ b/"
		if (path == "/dev/null") { path = ""; next }
		if (prefix != "") {
			if (index(path, prefix) != 1) { path = ""; next }
			path = substr(path, length(prefix) + 1)
		}
		next
	}
	/^@@ / {
		if (path == "") next
		match($0, /\+[0-9]+(,[0-9]+)?/)
		spec = substr($0, RSTART + 1, RLENGTH - 1)
		split(spec, parts, ",")
		start = parts[1] + 0
		count = (2 in parts) ? parts[2] + 0 : 1
		if (count == 0) {
			if (start < 1) start = 1
			print path, start, start
		} else {
			print path, start, start + count - 1
		}
	}
' <<<"$DIFF_RAW")"

if [[ -z "$RANGES" ]]; then
	echo "mutesting: no changed Go lines against $BASE (merge base ${MERGE_BASE:0:12}); nothing to mutate."
	exit 0
fi

FILES="$(awk '{print $1}' <<<"$RANGES" | sort -u)"

# Resolve each changed file to its Bazel package, then ask the graph which
# targets own it.
file_labels=()
skipped_files=()
while IFS= read -r f; do
	[[ -n "$f" ]] || continue
	if [[ ! -f "$f" ]]; then
		continue
	fi
	dir="$(dirname "$f")"
	pkg=""
	while true; do
		if [[ -f "$dir/BUILD.bazel" || -f "$dir/BUILD" ]]; then
			pkg="$dir"
			break
		fi
		[[ "$dir" == "." || "$dir" == "/" ]] && break
		dir="$(dirname "$dir")"
	done
	if [[ -z "$pkg" ]]; then
		skipped_files+=("$f")
		continue
	fi
	if [[ "$pkg" == "." ]]; then
		file_labels+=("//:${f}")
	else
		file_labels+=("//${pkg}:${f#"$pkg"/}")
	fi
done <<<"$FILES"

if [[ ${#skipped_files[@]} -gt 0 ]]; then
	for f in "${skipped_files[@]}"; do
		echo "mutesting: warning: $f is in no Bazel package; skipping." >&2
	done
fi

if [[ ${#file_labels[@]} -eq 0 ]]; then
	echo "mutesting: no changed Go file belongs to a Bazel package; nothing to mutate."
	exit 0
fi

label_set="$(
	IFS=' '
	echo "${file_labels[*]}"
)"

# Targets that list a changed file in an attribute. Libraries are mapped on to
# the tests that embed them, since mutants can only be killed by running tests.
query="let files = set($label_set) in
       let libs = kind('go_library rule', same_pkg_direct_rdeps(\$files)) in
       kind('go_test rule', same_pkg_direct_rdeps(\$files))
       + kind('go_test rule', same_pkg_direct_rdeps(\$libs))"

TARGETS="$(bazel query "$query" --keep_going --output=label 2>/dev/null | sort -u || true)"

if [[ -z "$TARGETS" ]]; then
	echo "mutesting: changed Go files are in no go_test target; nothing to mutate."
	echo "mutesting: (a package needs a go_test for mutants to be killable)"
	exit 0
fi

# The spec travels as a build setting value rather than a file, which keeps it
# free of any repository or workspace setup on the consumer's side. It
# deliberately records no commit ids: two branches carrying the same edit
# should produce the same action, and therefore the same cache entry.
SPEC="$({
	printf '{"files":{'
	first=1
	while IFS= read -r f; do
		[[ -n "$f" ]] || continue
		[[ -f "$f" ]] || continue
		[[ $first -eq 1 ]] || printf ','
		first=0
		printf '"%s":[' "$f"
		awk -v file="$f" 'BEGIN { sep = "" }
			$1 == file { printf "%s[%d,%d]", sep, $2, $3; sep = "," }' <<<"$RANGES"
		printf ']'
	done <<<"$FILES"
	printf '}}'
})"

target_count="$(wc -l <<<"$TARGETS" | tr -d ' ')"
file_count="$(wc -l <<<"$FILES" | tr -d ' ')"
echo "mutesting: $file_count changed Go file(s) since ${MERGE_BASE:0:12}, $target_count target(s) to mutate:"
sed 's/^/  /' <<<"$TARGETS"

bazel_args=(
	build
	--aspects=@rules_go_mutesting//mutesting:aspect.bzl%go_mutesting_aspect
	--output_groups=+mutesting_report
	"--@rules_go_mutesting//mutesting:changed_lines_spec=$SPEC"
)
while IFS= read -r t; do
	[[ -n "$t" ]] && bazel_args+=("$t")
done <<<"$TARGETS"
if [[ ${#PASSTHROUGH[@]} -gt 0 ]]; then
	bazel_args+=("${PASSTHROUGH[@]}")
fi

if [[ $DRY_RUN -eq 1 ]]; then
	echo
	echo "mutesting: changed line spec:"
	echo "  $SPEC"
	echo
	echo "mutesting: would run:"
	echo "  bazel ${bazel_args[*]}"
	exit 0
fi

exec bazel "${bazel_args[@]}"
