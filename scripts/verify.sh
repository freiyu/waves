#!/bin/sh
# Runs every test suite and prints one line per suite. The gate-bypass suite
# runs first. Exit status is non-zero if any suite failed.
set -u
GO=${GO:-go}
cd "$(dirname "$0")/.."
logs=$(mktemp -d)
failed=0
results=""

run() {
	name=$1
	shift
	start=$(date +%s)
	if "$@" >"$logs/$name.log" 2>&1; then
		status=PASS
	else
		status=FAIL
		failed=1
	fi
	results="$results$(printf '%-6s %-18s %4ss  %s' "$status" "$name" "$(($(date +%s) - start))" "$logs/$name.log")
"
	printf '%-6s %s\n' "$status" "$name"
}

run web-build make web-build
run gate-bypass $GO test -tags e2e -count=1 -v -run '^TestGateBypass$' ./internal/e2e
grep -E 'PASS |FAIL |gate-bypass:' "$logs/gate-bypass.log" | sed 's/^ *//'
run go-unit $GO test -race -count=1 ./...
run go-vet sh -c "gofmt -l . | (! grep .) && $GO vet ./... && $GO vet -tags e2e ./internal/e2e"
run e2e-binary $GO test -tags e2e -count=1 -v ./internal/e2e
grep -E '^\s+\|' "$logs/e2e-binary.log" | sed 's/^ *//'
# A skipped end-to-end test (for example Playwright without npx) is not a pass.
if grep -q -- '--- SKIP' "$logs/e2e-binary.log"; then
	grep -- '--- SKIP' "$logs/e2e-binary.log"
	echo "FAIL   e2e-binary (tests were skipped)"
	failed=1
fi
run web-check make web-check
run web-unit make web-test
run web-e2e-mock make web-e2e
run check-release make check-release

printf '\n%s' "$results"
[ "$failed" = 0 ] && echo "All suites passed." || echo "Some suites FAILED; see the logs above."
exit $failed
