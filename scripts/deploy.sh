#!/usr/bin/env bash
set -euo pipefail

SOURCE=/var/www/mcp
PRODUCTION=/var/www/mcp/production
SERVICE=mcp
HEALTH_URL=https://mcp.ai.msheriff.com/health

if [[ ${EUID} -ne 0 ]]; then
  echo "Run with sudo: sudo $0" >&2
  exit 1
fi

exec 9>/run/lock/mcp-deploy.lock
if ! flock -n 9; then
  echo "Another MCP deployment is already running." >&2
  exit 1
fi

build_tmp=$(mktemp /tmp/mcp-server-build.XXXXXX)
install_tmp=$(mktemp "$PRODUCTION/.mcp-server.new.XXXXXX")
backup=""
cleanup() {
  rm -f "$build_tmp" "$install_tmp"
}
trap cleanup EXIT

cd "$SOURCE"
echo "[1/9] Running Go tests"
go test ./...

echo "[2/9] Building six downloadable platform binaries"
make downloads

echo "[3/9] Building production binary"
CGO_ENABLED=0 go build -buildvcs=false -trimpath -o "$build_tmp" ./cmd/server

echo "[4/9] Validating binary"
version=$($build_tmp -version)
[[ -n "$version" ]]

echo "[5/9] Installing web and download assets"
install -d -m 0755 "$PRODUCTION/web" "$PRODUCTION/config" "$PRODUCTION/downloads"
install -m 0644 "$SOURCE/web/index.html" "$PRODUCTION/web/index.html"
install -m 0644 "$SOURCE/web/launch-banner.svg" "$PRODUCTION/web/launch-banner.svg"
install -m 0644 "$SOURCE/web/openai-test.html" "$PRODUCTION/web/openai-test.html"
install -m 0644 "$SOURCE/web/openai-test.js" "$PRODUCTION/web/openai-test.js"
for artifact in \
  mcp-server-windows-amd64.exe mcp-server-windows-arm64.exe \
  mcp-server-linux-amd64 mcp-server-linux-arm64 \
  mcp-server-macos-amd64 mcp-server-macos-arm64 SHA256SUMS; do
  artifact_tmp=$(mktemp "$PRODUCTION/downloads/.${artifact}.XXXXXX")
  install -m 0644 "$SOURCE/dist/$artifact" "$artifact_tmp"
  mv "$artifact_tmp" "$PRODUCTION/downloads/$artifact"
done

echo "[6/9] Atomically installing binary version $version"
install -m 0755 "$build_tmp" "$install_tmp"
if [[ -f "$PRODUCTION/mcp-server" ]]; then
  backup="$PRODUCTION/mcp-server.rollback"
  cp -a "$PRODUCTION/mcp-server" "$backup"
fi
mv "$install_tmp" "$PRODUCTION/mcp-server"
version_tmp=$(mktemp "$PRODUCTION/.VERSION.XXXXXX")
printf '%s\n' "$version" > "$version_tmp"
chmod 0644 "$version_tmp"
mv "$version_tmp" "$PRODUCTION/VERSION"

echo "[7/9] Restarting $SERVICE.service"
if ! systemctl restart "$SERVICE"; then
  if [[ -n "$backup" && -f "$backup" ]]; then
    mv "$backup" "$PRODUCTION/mcp-server"
    systemctl restart "$SERVICE" || true
  fi
  exit 1
fi

echo "[8/9] Checking systemd status"
systemctl is-active --quiet "$SERVICE"
systemctl --no-pager --full status "$SERVICE" | head -n 20

echo "[9/9] Checking HTTPS health"
health=""
for _ in $(seq 1 15); do
  if health=$(curl --fail --silent "$HEALTH_URL" 2>/dev/null); then
    break
  fi
  sleep 2
done
if [[ -z "$health" ]]; then
  curl --fail --silent --show-error "$HEALTH_URL"
  echo
  exit 1
fi
printf '%s\n' "$health"

rm -f "$backup"
echo "Deployment succeeded: $version"
