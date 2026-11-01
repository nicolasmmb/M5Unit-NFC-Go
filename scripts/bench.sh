#!/usr/bin/env bash
# Benchmark helpers for M5Unit-GPS-Go.
#
# Usage:
#   scripts/bench.sh run [out]        run every benchmark (default: bench/current.txt)
#   scripts/bench.sh save <name>      run and save as bench/<name>.txt
#   scripts/bench.sh compare <a> <b>  benchstat between two saved runs
#   scripts/bench.sh alloc            allocation/footprint contract tests
#   scripts/bench.sh profile [out]    CPU + memory profiles with pprof summaries
#   scripts/bench.sh escape           show heap escapes (compiler diagnostics)
#
# Environment:
#   COUNT=8        -count passed to `go test -bench`
#   BENCHTIME=1s   -benchtime
#   BENCH=Feed     -bench regexp (default: everything)
#
# Examples:
#   scripts/bench.sh save before
#   # ... edit the code ...
#   scripts/bench.sh save after
#   scripts/bench.sh compare before after
set -euo pipefail

cd "$(dirname "$0")/.."

COUNT="${COUNT:-8}"
BENCHTIME="${BENCHTIME:-1s}"
BENCH="${BENCH:-.}"
OUTDIR="bench"

run_benchstat() {
	local bin
	if command -v benchstat >/dev/null 2>&1; then
		bin="$(command -v benchstat)"
	elif [ -x "$(go env GOPATH)/bin/benchstat" ]; then
		bin="$(go env GOPATH)/bin/benchstat"
	else
		echo "benchstat not found; install it with:" >&2
		echo "  go install golang.org/x/perf/cmd/benchstat@latest" >&2
		return 1
	fi
	"$bin" "$@"
}

run_bench() {
	local out="$1"
	mkdir -p "$OUTDIR"
	go test -run '^$' -bench "$BENCH" -benchmem -count "$COUNT" \
		-benchtime "$BENCHTIME" ./... | tee "$out"
}

cmd="${1:-run}"
case "$cmd" in
run)
	out="${2:-$OUTDIR/current.txt}"
	run_bench "$out"
	echo
	echo "saved: $out"
	;;
save)
	name="${2:?usage: scripts/bench.sh save <name>}"
	out="$OUTDIR/$name.txt"
	run_bench "$out"
	echo
	echo "saved: $out"
	;;
compare)
	a="${2:?usage: scripts/bench.sh compare <before> <after>}"
	b="${3:?usage: scripts/bench.sh compare <before> <after>}"
	[ -f "$a" ] || a="$OUTDIR/$a.txt"
	[ -f "$b" ] || b="$OUTDIR/$b.txt"
	run_benchstat "$a" "$b"
	;;
alloc)
	go test -run 'Alloc|Footprint' -v ./...
	;;
profile)
	out="${2:-$OUTDIR/current.prof}"
	mkdir -p "$OUTDIR"
	go test -run '^$' -bench "$BENCH" -benchtime "$BENCHTIME" \
		-cpuprofile "$out.cpu" -memprofile "$out.mem" ./... >/dev/null
	echo "== CPU (top) =="
	go tool pprof -top -nodecount=12 "$out.cpu" 2>/dev/null | sed -n '1,20p'
	echo "== allocations (top) =="
	go tool pprof -top -alloc_space -nodecount=12 "$out.mem" 2>/dev/null | sed -n '1,20p'
	;;
escape)
	go build -gcflags='-m' . 2>&1 | grep -E 'escapes to heap|moved to heap' || echo "no heap escapes"
	;;
*)
	echo "unknown command: $cmd" >&2
	sed -n '2,25p' "$0" >&2
	exit 2
	;;
esac
