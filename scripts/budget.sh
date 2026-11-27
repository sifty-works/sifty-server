#!/usr/bin/env bash
set -euo pipefail

cd "$(git -C "$(dirname "$0")" rev-parse --show-toplevel)"

files() {
  git ls-files --cached --others --exclude-standard -- "$@"
}

count() {
  local n=0 f
  while IFS= read -r f; do
    [ -f "$f" ] || continue
    n=$((n + $(grep -cv '^[[:space:]]*$' "$f" || true)))
  done
  echo "$n"
}

fail=0
printf "%-40s %10s %10s\n" "Measure" "Actual" "Budget"
printf "%-40s %10s %10s\n" "-------" "------" "------"

be=$(files '*.go' | grep -v '_test\.go$' | count)
be_test=$(files '*_test.go' | count)

row() { printf "%-40s %10s %10s %s\n" "$1" "$2" "$3" "$4"; }
over() { if [ "$1" -gt "$2" ]; then echo "OVER"; fi; }

row "Backend code (lines)" "$be" "20000" "$(over "$be" 20000)"
row "Backend tests (lines)" "$be_test" "-" ""
if [ "$be" -gt 20000 ]; then fail=1; fi

printf "\n%-40s %10s %10s\n" "List API measure" "Actual" "Budget"
printf "%-40s %10s %10s\n" "----------------" "------" "------"
measure_out=$(SIFTY_MEASURE=1 go test -v -run TestListMeasure -count=1 \
  ./internal/inbox/... 2>&1)
no_q=$(echo "$measure_out" | grep 'list p95 no-query:' | awk '{print $NF}')
text=$(echo "$measure_out" | grep 'list p95 text-query:' | awk '{print $NF}')
fitf=$(echo "$measure_out" | grep 'list p95 fit-filter:' | awk '{print $NF}')
if [ -z "$no_q" ] || [ -z "$text" ] || [ -z "$fitf" ]; then
  echo "  measure test did not produce output" >&2
else
  printf "%-40s %10s %10s\n" "  p95 no-query" "$no_q" "50ms"
  printf "%-40s %10s %10s\n" "  p95 text-query" "$text" "50ms"
  printf "%-40s %10s %10s\n" "  p95 Best fit with Fit filter" "$fitf" "50ms"
fi

if [ "$fail" -ne 0 ]; then
  echo "A budget is exceeded." >&2
  exit 1
fi
