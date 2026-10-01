#!/usr/bin/env bash
set -euo pipefail

domain=${1:?Usage: sudo ./certbot.example.sh mcp.example.com}

# This is a small example, not a replacement for an existing certificate
# management script. Back up and extend your current configuration.
certbot --nginx --redirect -d "$domain"
