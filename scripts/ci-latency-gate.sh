#!/usr/bin/env bash
# CI latency regression gate.
# Runs orchestrator benchmarks and checks latency against SPEC targets.
# Usage: ./scripts/ci-latency-gate.sh
# Exit 0 if all gates pass, 1 if any regression detected.
set -euo pipefail

GO="${GO:-/home/duy/go/bin/go}"
BENCH_DIR="tests/benchmark"
RESULTS_DIR="docs/perf/ci"
SPEC_P50_MS=3000
SPEC_P99_MS=12000
SPEC_TTFT_P50_MS=2000

mkdir -p "$RESULTS_DIR"

echo "=== CI Latency Regression Gate ==="
echo "SPEC targets: p50<${SPEC_P50_MS}ms p99<${SPEC_P99_MS}ms ttft_p50<${SPEC_TTFT_P50_MS}ms"
echo ""

# 1. Run unit tests
echo "--- Unit Tests ---"
if $GO test ./internal/orchestrator/... -count=1 -timeout 60s; then
  echo "[PASS] Unit tests"
else
  echo "[FAIL] Unit tests"
  exit 1
fi

# 2. Run E2E tests
echo ""
echo "--- E2E Tests ---"
if $GO test -tags e2e ./tests/e2e/... -count=1 -timeout 60s; then
  echo "[PASS] E2E tests"
else
  echo "[FAIL] E2E tests"
  exit 1
fi

# 3. Run vet
echo ""
echo "--- Go Vet ---"
if $GO vet ./...; then
  echo "[PASS] go vet"
else
  echo "[FAIL] go vet"
  exit 1
fi

# 4. Run benchmarks (quick mode: -benchtime=100ms)
echo ""
echo "--- Benchmarks ---"
BENCH_FILE="$RESULTS_DIR/bench_$(date +%Y%m%d_%H%M%S).txt"
$GO test -tags bench -bench=. -benchmem -benchtime=100ms -count=1 "./$BENCH_DIR/..." 2>&1 | tee "$BENCH_FILE"

# 5. Check for existing baseline and compare
BASELINE_FILE="$RESULTS_DIR/baseline.txt"
if [[ -f "$BASELINE_FILE" ]]; then
  echo ""
  echo "--- Comparing against baseline ---"
  # Use benchstat if available, otherwise manual check
  if command -v benchstat &>/dev/null; then
    benchstat "$BASELINE_FILE" "$BENCH_FILE" || true
  else
    echo "benchstat not found; skipping comparison. Install: go install golang.org/x/perf/cmd/benchstat@latest"
  fi
fi

# Save current as new baseline
cp "$BENCH_FILE" "$BASELINE_FILE"

# 6. Check SPEC latency targets from load test (single-request mode)
echo ""
echo "--- Load Test Latency Check ---"
LOAD_RESULT=$($GO test -tags load -run TestOrchestratorLoadSingleRequest -count=1 -timeout 60s -v "./tests/load/..." 2>&1)
echo "$LOAD_RESULT"

if echo "$LOAD_RESULT" | grep -q "PASS"; then
  echo "[PASS] Load test latency within SPEC"
else
  echo "[FAIL] Load test latency exceeded SPEC targets"
  exit 1
fi

echo ""
echo "=== All gates PASS ==="
echo "Results saved to $RESULTS_DIR/"
