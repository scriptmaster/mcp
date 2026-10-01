#!/usr/bin/env bash
set -euo pipefail

URL=${MCP_URL:-https://mcp.ai.msheriff.com/mcp}
if [[ -z ${MCP_TOKEN:-} ]]; then
  if [[ ${EUID} -eq 0 && -r /etc/mcp/mcp.env ]]; then
    MCP_TOKEN=$(sed -n 's/^MCP_TOKEN=//p' /etc/mcp/mcp.env | head -n 1)
  else
    echo "Set MCP_TOKEN or run with sudo." >&2
    exit 1
  fi
fi

curl --fail --silent --show-error "$URL" \
  -H "Authorization: Bearer $MCP_TOKEN" \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  --data '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"operator-check","version":"1.0"}}}'
echo
curl --fail --silent --show-error "$URL" \
  -H "Authorization: Bearer $MCP_TOKEN" \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  --data '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'
echo
