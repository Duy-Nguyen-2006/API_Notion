#!/usr/bin/env bash
# Smoke test for orchestrator endpoints (Notion reasoner + mimo executor).
# Usage:
#   NOTION_API_KEY=... MIMO_API_KEY=... ./scripts/smoke-endpoints.sh
# Legacy: API_KEY sets both when NOTION_/MIMO_ unset.
# Exit 0 if both endpoints healthy, 1 otherwise.
set -euo pipefail

API_KEY="${API_KEY:-}"
NOTION_API_KEY="${NOTION_API_KEY:-${API_KEY}}"
MIMO_API_KEY="${MIMO_API_KEY:-${API_KEY}}"
if [[ -z "$NOTION_API_KEY" || -z "$MIMO_API_KEY" ]]; then
  echo "Set NOTION_API_KEY and MIMO_API_KEY (or API_KEY for both)." >&2
  exit 2
fi
MIMO_URL="${MIMO_URL:-https://api.lgmmo.click/v1/chat/completions}"
NOTION_URL="${NOTION_URL:-https://notion.lgmmo.click/v1/chat/completions}"
SAMPLES="${SAMPLES:-10}"

echo "=== Endpoint Smoke Test ==="
echo "Mimo:   $MIMO_URL"
echo "Notion: $NOTION_URL"
echo "Samples: $SAMPLES"
echo ""

pass=0
fail=0

probe() {
  local name="$1" url="$2" model="$3" api_key="$4"
  local code time_total ttft
  local out
  out=$(curl -s -w '\n%{http_code}\n%{time_total}\n%{time_starttransfer}' \
    -X POST "$url" \
    -H "Authorization: Bearer $api_key" \
    -H "Content-Type: application/json" \
    -d "{\"model\":\"$model\",\"stream\":false,\"messages\":[{\"role\":\"user\",\"content\":\"ping\"}],\"max_tokens\":10}" \
    2>&1) || true
  code=$(echo "$out" | tail -3 | head -1)
  time_total=$(echo "$out" | tail -2 | head -1)
  ttft=$(echo "$out" | tail -1)
  if [[ "$code" == "200" ]]; then
    echo "  [PASS] $name HTTP=$code total=${time_total}s ttft=${ttft}s"
    return 0
  else
    echo "  [FAIL] $name HTTP=$code total=${time_total}s"
    return 1
  fi
}

rtt_samples() {
  local name="$1" url="$2" model="$3" api_key="$4"
  local times=()
  for i in $(seq 1 "$SAMPLES"); do
    local ttft
    ttft=$(curl -s -o /dev/null -w '%{time_starttransfer}' \
      -X POST "$url" \
      -H "Authorization: Bearer $api_key" \
      -H "Content-Type: application/json" \
      -d "{\"model\":\"$model\",\"stream\":false,\"messages\":[{\"role\":\"user\",\"content\":\"ping $i\"}],\"max_tokens\":10}" \
      2>&1) || ttft="99.0"
    times+=("$ttft")
  done
  # Sort and report p50/p99
  local sorted
  sorted=$(printf '%s\n' "${times[@]}" | sort -n)
  local p50 p99 count
  count=${#times[@]}
  p50=$(echo "$sorted" | sed -n "$((count/2))p")
  p99=$(echo "$sorted" | sed -n "$((count*99/100))p")
  echo "  $name RTT: p50=${p50}s p99=${p99}s (n=$count)"
  # Save baseline
  echo "{\"endpoint\":\"$url\",\"model\":\"$model\",\"samples\":$count,\"p50_s\":$p50,\"p99_s\":$p99}" > "docs/perf/${name}_baseline.json"
}

mkdir -p docs/perf

echo "--- Smoke Calls ---"
if probe "mimo" "$MIMO_URL" "mimo-v2.5-pro" "$MIMO_API_KEY"; then
  pass=$((pass+1))
else
  fail=$((fail+1))
fi
if probe "notion" "$NOTION_URL" "opus-4.8" "$NOTION_API_KEY"; then
  pass=$((pass+1))
else
  fail=$((fail+1))
fi

echo ""
echo "--- RTT Baseline ($SAMPLES samples) ---"
rtt_samples "mimo" "$MIMO_URL" "mimo-v2.5-pro" "$MIMO_API_KEY" || true
rtt_samples "notion" "$NOTION_URL" "opus-4.8" "$NOTION_API_KEY" || true

echo ""
echo "=== Results: $pass PASS, $fail FAIL ==="
if [[ $fail -gt 0 ]]; then
  echo "Some endpoints are down. Baseline saved to docs/perf/ anyway."
  exit 1
fi
echo "All endpoints healthy. Baseline saved to docs/perf/."
