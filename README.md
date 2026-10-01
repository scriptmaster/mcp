# Remote Development MCP Server

A production Go MCP server that gives an authenticated AI client bounded tools for exploring and operating configured source-code repositories on this VPS.

## Start here

This repository is the readable source for the service running at
`mcp.ai.msheriff.com`. In plain English, it lets an authorized AI assistant
look at selected code folders, make controlled changes, run tests, use Git, and
request a deployment. It also includes a small authenticated OpenAI prompt API.

If you are new to Go, read these in order:

1. [Beginner code walkthrough](docs/CODE_WALKTHROUGH.md) — every source file
   and every continuous line range explained in plain language.
2. [Security review](docs/SECURITY_REVIEW.md) — protections, scan results, and
   risks that an operator still needs to manage.
3. `config.example.yaml` — the settings you copy and adapt for your server.

Clone it with:

```bash
git clone https://github.com/fs-sheriff/remote-development-mcp.git
cd remote-development-mcp
go test ./...
```

Compiled binaries and real secrets are deliberately not stored in GitHub.
The GitHub repository is public, but the running MCP and OpenAI endpoints remain
authenticated.

Production URLs:

- Homepage/operator manual: `https://mcp.ai.msheriff.com/`
- Health: `https://mcp.ai.msheriff.com/health`
- MCP: `https://mcp.ai.msheriff.com/mcp`
- OpenAI prompt API: `https://mcp.ai.msheriff.com/openai/prompt`
- OpenAI browser test: `https://mcp.ai.msheriff.com/openai/test`

## Architecture

```text
MCP client
   │ HTTPS + Authorization: Bearer <token>
   ▼
NGINX :443
   │ localhost HTTP
   ▼
mcp.service → /var/www/mcp/production/mcp-server
   │ validates configured workspace + relative path + symlinks
   ▼
configured repository → bounded file/search/git/command/deploy operations
```

The source tree and production artifacts are separate:

```text
/var/www/mcp/                 source
/var/www/mcp/production/      deployed binary, config, and web assets
/etc/mcp/mcp.env              protected bearer token
/etc/systemd/system/mcp.service
/etc/nginx/sites-available/mcp.ai.msheriff.com.conf
```

## Transport and protocol

The server implements **stateless Streamable HTTP** at `POST /mcp`, not the obsolete SSE-only transport. It targets MCP protocol revision `2026-07-28`, including deterministic tool discovery and complete-result fields, and negotiates initialization compatibility with `2025-11-25`, `2025-06-18`, and `2025-03-26` clients. It validates `Mcp-Method` and `Mcp-Name` whenever clients supply the current routing headers. The service returns JSON responses directly; `GET /mcp` returns `405` because this server does not emit server-initiated notifications.

The choice follows the current [MCP Streamable HTTP specification](https://modelcontextprotocol.io/specification/2026-07-28/basic/transports) and OpenAI's [MCP and connectors guide](https://developers.openai.com/api/docs/guides/tools-connectors-mcp), which supports remote Streamable HTTP MCP servers.

## Security model

- Every request to `/mcp` requires `Authorization: Bearer <TOKEN>`; `/` and `/health` are public.
- Token comparisons are constant-time. Tokens are generated with OpenSSL, stored in `/etc/mcp/mcp.env` with mode `0600`, never accepted in URLs, and never logged.
- Only configured, enabled workspaces exist from the client's perspective. Server filesystem paths are not returned by `workspace_list`.
- All paths must be relative, remain within the selected workspace after symlink resolution, and avoid protected/sensitive names. Absolute paths, `../`, symlink escapes, `.env`, keys, certificates, and configured protected paths are rejected.
- File reads, writes, search results, requests, stdout, stderr, and timeouts are bounded.
- Replacement writes require the current SHA-256. Patches require an exact unique old-text match. Deletes require SHA-256 and only affect regular files.
- There is no arbitrary shell-string MCP tool. `run_command` accepts only administrator-configured names mapped to argv arrays. Command children receive a scrubbed environment without `MCP_TOKEN`.
- Git force push, hard reset, branch deletion, and history rewriting are not implemented. Pull is fast-forward only. Push and deploy are separate explicit calls.
- The systemd service uses a dedicated `mcp` user, no capabilities, `NoNewPrivileges`, filesystem protection, private temporary storage, restricted address families, and inaccessible `/root`, SSH, certificate, and shadow paths.
- Browser `Origin` values are allowlisted; server-to-server requests without `Origin` are accepted.
- `POST /openai/prompt` has separate `X-API-Key` and `X-API-Secret` authentication, a request-size limit, an output-token limit, a timeout, and a per-minute request limit. The upstream OpenAI key remains server-side and is never returned or logged.

This is a powerful repository automation service. Configure only repositories you intend an AI client to access, use a dedicated OS user, keep high-impact ChatGPT actions approval-gated, and audit `journalctl -u mcp`.

## Configuration

The installed configuration is `production/config/config.yaml`; it is created
from `config.example.yaml` during server setup and is not included in the
public repository. Secrets are always supplied through the environment:

```yaml
server:
  listen: "127.0.0.1:8095"
security:
  token: "FROM_ENVIRONMENT"
openai:
  enabled: true
  model: "gpt-6-luna"
  api_key: "FROM_ENVIRONMENT"
  client_key: "FROM_ENVIRONMENT"
  client_secret: "FROM_ENVIRONMENT"
  allowed_origins:
    - "https://mcp.ai.msheriff.com"
    - "https://*.app.ai.msheriff.com"
  max_prompt_bytes: 16384
  max_output_tokens: 1200
  timeout_seconds: 90
  requests_per_minute: 30
commands:
  build: true
  test: true
  git: true
  deploy: true
  run: true
workspaces:
  - name: example
    path: /var/www/example
    enabled: true
    read_only: false
    protected_paths: [production, .secrets]
    build:
      command: ["make", "build"]
      timeout: 300
    test:
      command: ["make", "test"]
      timeout: 300
    deploy:
      command: "./deploy.sh"
      timeout: 600
    run_commands:
      lint:
        command: ["make", "lint"]
        timeout: 180
```

Command lists execute directly without a shell. A simple scalar such as `./deploy.sh` is also accepted and split into argv; use a YAML list for arguments or values containing spaces. A workspace can be disabled with `enabled: false` or made inspection-only with `read_only: true`.

## OpenAI prompt API

`POST /openai/prompt` validates the DevSpectra client headers and sends only the supplied prompt to OpenAI's Responses API. It accepts:

```json
{"prompt":"Reply with exactly: MCP OpenAI integration OK"}
```

Required headers are `Content-Type: application/json`, `X-API-Key`, and `X-API-Secret`. Successful responses contain `response_id`, `model`, `text`, `request_id`, `usage`, and `duration_ms`. The API secret and upstream OpenAI key are loaded only from `/etc/mcp/mcp.env`; neither is committed to source or shown on the public documentation page.

The manual browser client at `GET /openai/test` keeps entered credentials in page memory only. It is suitable for an owner test. A production browser bundle must not embed the shared secret because end users can extract browser-delivered credentials; DevSpectra should call `POST /openai/prompt` from its own backend whenever possible.

When adding a repository, grant the `mcp` OS user only the required access and add the repository to `ReadWritePaths=` in the systemd unit. Then run:

```bash
sudo systemctl daemon-reload
sudo systemctl restart mcp
sudo journalctl -u mcp -n 50 --no-pager
```

## MCP tools

| Tool | Behavior |
|---|---|
| `workspace_list` | Lists configured enabled workspaces without absolute paths. |
| `tree` | Bounded directory tree with depth and result limits. |
| `file_find` | Filename substring/glob discovery. |
| `code_search` | Ripgrep regex search with scope, glob, result limit, and context lines. |
| `read_file` | UTF-8 file read with optional inclusive line range and SHA-256. |
| `write_file` | Create; guarded overwrite with SHA-256. |
| `apply_patch` | Unique exact-text replacement and concise diff. |
| `delete_file` | Hash-guarded regular-file deletion. |
| `git_status` | Concise branch/worktree status. |
| `git_diff` | Staged or unstaged diff, optionally path-scoped. |
| `git_log` | Bounded concise history. |
| `git_fetch` | Fetch/prune one or all remotes. |
| `git_pull` | Explicit fast-forward-only pull. |
| `git_add` | Explicit staging of validated paths. |
| `git_commit` | Explicit commit of staged changes. |
| `git_push` | Separate explicit non-force push. |
| `build` | Configured build or Go/npm/dotnet/make/CMake detection. |
| `test` | Configured test or safe project detection. |
| `run_command` | One named administrator-approved argv operation. |
| `deploy` | One explicit workspace-specific configured deployment operation. |

Every command result contains `stdout`, `stderr`, `exit_code`, `duration_ms`, timeout state, truncation state, command argv, and relative working directory.

### Build detection

- `go.mod`: `go build ./...` / `go test ./...`
- `package.json`: only existing `build` / `test` scripts
- `*.sln` or `*.csproj`: `dotnet build` / `dotnet test`
- `Makefile`: `make build` / `make test`
- `CMakeLists.txt`: uses an existing `build` directory for builds; otherwise requires explicit configuration

Detection never runs package installation, CMake configuration, clean targets, or destructive commands.

## Build and test

```bash
cd /var/www/mcp
go mod download
gofmt -w cmd internal
go test ./...
go build -buildvcs=false -o /tmp/mcp-server ./cmd/server
/tmp/mcp-server -version
```

### Release downloads

Build all six supported release binaries and their checksum manifest with:

```bash
make downloads
```

Individual targets are available when only one artifact is needed:

```bash
make windows-amd64
make windows-arm64
make linux-amd64
make linux-arm64
make macos-amd64
make macos-arm64
```

Artifacts are written to `dist/`. The production deployment script atomically installs them into `production/downloads/`, where the server exposes only the exact allowlisted filenames under `GET /downloads/<filename>`. Verify local artifacts with:

```bash
cd /var/www/mcp/dist
sha256sum -c SHA256SUMS
```

Security-focused tests cover authentication success/failure, unauthorized MCP access, workspace enablement, traversal and absolute escapes, symlink escape, line-range reads, search limits, command timeout/output limits, and patch uniqueness.

## Production deployment

```bash
sudo /var/www/mcp/scripts/deploy.sh
```

The script runs tests, builds the six platform downloads and checksum manifest, builds a temporary native static binary, validates `-version`, installs web/download assets, atomically moves the service binary into production, writes `VERSION`, restarts `mcp.service`, verifies systemd state, and checks the HTTPS health endpoint. It keeps a rollback copy until health succeeds.

## Token management

```bash
sudo /var/www/mcp/scripts/mcp-token get
sudo /var/www/mcp/scripts/mcp-token rotate
```

Rotation changes only `MCP_TOKEN`, preserves unrelated settings such as
`OPENAI_API_KEY`, and restarts the service. Update clients after rotation.
Never paste the token into the public homepage, a repository file, a URL/query
string, a ticket, or logs.

## systemd

The source unit is `docs/mcp.service`; installed location is `/etc/systemd/system/mcp.service`.

```bash
sudo systemctl daemon-reload
sudo systemctl enable mcp
sudo systemctl restart mcp
systemctl status mcp
journalctl -u mcp -f
journalctl -u mcp --since today
```

## NGINX and SSL

The source vhost is `docs/nginx-mcp.conf`; installed locations are `/etc/nginx/sites-available/mcp.ai.msheriff.com.conf` and the corresponding `sites-enabled` symlink. NGINX terminates TLS, redirects HTTP to HTTPS, forwards authentication and MCP headers, disables proxy buffering, and applies one-hour streaming/command timeouts.

The existing `/root/certbot.sh` domain list is preserved and extended with `mcp.ai.msheriff.com`. Before any edit it is backed up to a timestamped `/root/certbot.sh.bak-*` file.

```bash
sudo nginx -t
sudo systemctl reload nginx
sudo certbot certificates
curl https://mcp.ai.msheriff.com/health
```

## Connect to ChatGPT

Current OpenAI guidance calls custom MCP integrations **apps**. Full MCP, including write actions, is available in developer mode for ChatGPT Business and Enterprise/Edu on the web; Pro may be limited to read/search. See OpenAI's current [Developer mode and MCP apps in ChatGPT](https://help.openai.com/en/articles/12584461-developer-mode-and-full-mcp-connectors-in-chatgpt).

1. A workspace admin enables **Workspace Settings → Permissions & Roles → Connected Data → Developer mode / Create custom MCP connectors**.
2. Enable **Settings → Apps → Advanced Settings → Developer mode** for the authorized account when shown.
3. Admin/owner: **Workspace Settings → Apps → Create**. Authorized user: **Settings → Apps → Create**.
4. Name the app `Remote Development MCP` and use `https://mcp.ai.msheriff.com/mcp`.
5. Select the bearer/access-token authentication mechanism if your ChatGPT tenant offers it. Retrieve the secret with `sudo /var/www/mcp/scripts/mcp-token get` and supply it as the credential, never as a URL query parameter.
6. Select **Scan Tools**, review all actions, and then **Create**. It appears under **Settings → Apps → Enabled Apps** with a `Dev` label.
7. Enable required write actions in action controls. Keep `delete_file`, `git_push`, and `deploy` subject to explicit confirmation.
8. Open a new chat, select the draft app from the tools menu, and verify it.

Static bearer authentication is an MCP/client feature, but the current OpenAI ChatGPT help page does not promise that every tenant exposes a static bearer-entry option. If the creation UI offers only OAuth or no-auth, do not disable this server's authentication: use an MCP-compatible client that supports bearer headers (including the OpenAI Responses API `authorization` field), or add an OAuth 2.1 authorization layer before connecting ChatGPT.

Verification prompts:

```text
List the workspaces available through my development MCP server.

Search the <workspace> repository for LoginForm and show me the relevant files.

Show git status and current diff. Do not modify anything.

Run the project's tests and summarize any failures.
```

After tool definitions change, use the app's **Refresh/Scan Tools** control and review the action diff. ChatGPT may use a frozen approved snapshot until an admin refreshes it.

## Manual MCP verification

```bash
sudo /var/www/mcp/scripts/check-mcp.sh
```

Or retrieve the token into a shell variable without putting it in shell history:

```bash
read -rsp 'MCP token: ' MCP_TOKEN; echo
curl https://mcp.ai.msheriff.com/mcp \
  -H "Authorization: Bearer $MCP_TOKEN" \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  --data '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}'
unset MCP_TOKEN
```

## Troubleshooting

### 401

The token is absent/stale or the header format is wrong. Retrieve the current value and ensure the client sends `Authorization: Bearer <TOKEN>`. Rotating invalidates the old token immediately.

### 502

```bash
systemctl status mcp
journalctl -u mcp -n 100 --no-pager
curl http://127.0.0.1:8095/health
nginx -t
```

If localhost fails, fix the service/config. If localhost succeeds while HTTPS returns 502, inspect the NGINX vhost and error log.

### Timeout

Check the command result's timeout and truncation fields and `journalctl -u mcp`. Raise only the specific configured operation timeout. NGINX proxy timeouts are one hour.

### NGINX / TLS

```bash
nginx -T
tail -n 100 /var/log/nginx/error.log
certbot certificates
curl -Iv https://mcp.ai.msheriff.com/health
```

### systemd

```bash
systemctl cat mcp
systemctl show mcp -p User -p Group -p EnvironmentFiles
systemctl reset-failed mcp
systemctl restart mcp
journalctl -u mcp -n 200 --no-pager
```

The public homepage contains the same operational checklist in a compact operator-manual format and never exposes credentials.
