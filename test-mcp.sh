#!/usr/bin/env bash
set -euo pipefail

BASE_URL="${1:-https://yr.mcp.fn.flaatten.org}"
SSE_OUTPUT=$(mktemp)
trap 'kill $SSE_PID 2>/dev/null; rm -f "$SSE_OUTPUT"' EXIT

echo "=== yr MCP Service Test ==="
echo "URL: $BASE_URL"
echo ""

# Health check
echo -n "Health check... "
HEALTH=$(curl -sf "$BASE_URL/health")
[[ "$HEALTH" == "ok" ]] && echo "OK" || { echo "FAILED: $HEALTH"; exit 1; }

# Connect SSE in background
curl -sN "$BASE_URL/sse" > "$SSE_OUTPUT" 2>&1 &
SSE_PID=$!
sleep 2

# Extract session ID
SESSION_ID=$(grep -o 'sessionId=[^ ]*' "$SSE_OUTPUT" | head -1 | cut -d= -f2 | tr -d '[:space:]')
[[ -z "$SESSION_ID" ]] && { echo "Failed to get session ID"; cat "$SSE_OUTPUT"; exit 1; }
echo "Session: $SESSION_ID"
echo ""

LINES_SEEN=0

post() {
  curl -sf -X POST "$BASE_URL/message?sessionId=$SESSION_ID" \
    -H "Content-Type: application/json" -d "$1" || true
}

read_response() {
  sleep 3
  # Get new data lines from SSE stream since last read
  local all_data
  all_data=$(grep '^data: ' "$SSE_OUTPUT" | tail -n +$((LINES_SEEN + 1)))
  LINES_SEEN=$(grep -c '^data: ' "$SSE_OUTPUT" || true)
  local last
  last=$(echo "$all_data" | tail -1 | sed 's/^data: //')
  echo "$last" | python3 -m json.tool 2>/dev/null || echo "$last"
}

# Initialize
echo "--- Initialize ---"
post '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}'
read_response

# List tools
echo ""
echo "--- Tools ---"
post '{"jsonrpc":"2.0","id":2,"method":"tools/list"}'
read_response

# Get forecast for Bergen
echo ""
echo "--- Forecast: Bergen ---"
post '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"get_forecast","arguments":{"location":"Bergen"}}}'
read_response

# Get hourly for Oslo
echo ""
echo "--- Hourly: Oslo ---"
post '{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"get_hourly","arguments":{"location":"Oslo"}}}'
read_response

# Get precipitation by coordinates (Bergen)
echo ""
echo "--- Precipitation: 60.39,5.32 ---"
post '{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"get_precipitation","arguments":{"location":"60.39,5.32"}}}'
read_response

echo ""
echo "=== Done ==="
