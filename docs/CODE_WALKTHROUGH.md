# Beginner code walkthrough

This guide explains the complete source in plain language. The line ranges are
continuous: together they account for every line in each Go file, including
package declarations, imports, blank lines, comments, and closing braces.

## How a request travels

```text
internet → NGINX HTTPS → Go HTTP router → authentication
         → workspace/path checks → small service → JSON response
```

The program is intentionally split into small packages. Each package has one
job, which makes security rules easier to test.

## Executable entry point

### `cmd/server/main.go` — 61 lines

| Lines | Meaning |
|---:|---|
| 1–17 | Declares the executable and imports logging, HTTP, signals, config, and server packages. |
| 18–55 | `main` handles `-version`, loads configuration, starts the HTTP server, and shuts down gracefully after an operating-system signal. |
| 56–61 | `envOr` reads an environment variable and falls back to a safe default. |

## Authentication

### `internal/auth/auth.go` — 54 lines

| Lines | Meaning |
|---:|---|
| 1–9 | Imports SHA-256, constant-time comparison, HTTP, and string helpers. |
| 10–22 | Stores the MCP bearer token and validates the `Authorization: Bearer …` header. |
| 23–35 | Wraps protected routes and returns HTTP 401 without revealing why a token failed. |
| 36–49 | Stores and validates the separate `X-API-Key` plus `X-API-Secret` pair. |
| 50–54 | Hashes both values before constant-time comparison, avoiding length-based timing leaks. |

### `internal/auth/auth_test.go` — 51 lines

Lines 1–30 test correct and incorrect bearer tokens. Lines 31–51 test that both
OpenAI client headers must match.

## Configuration

### `internal/config/config.go` — 260 lines

| Lines | Meaning |
|---:|---|
| 1–13 | Imports YAML parsing, filesystem, validation, and time helpers. |
| 14–81 | Defines the YAML structure: server, security, OpenAI, limits, feature switches, workspaces, and allowed operations. |
| 82–103 | Parses commands as either a simple string or, preferably, a safe argument list. |
| 104–172 | Loads strict YAML, substitutes environment secrets, validates token length, workspace names/paths, duplicates, and configured operations. |
| 173–218 | Loads OpenAI secrets from environment variables and applies bounded defaults. |
| 219–231 | Rejects empty command programs, negative timeouts, and absolute working directories. |
| 232–260 | Supplies conservative defaults for localhost binding and all size/time/result limits. |

## Workspace boundary

### `internal/workspace/workspace.go` — 147 lines

| Lines | Meaning |
|---:|---|
| 1–15 | Defines imports and the two clear errors: escape and protected path. |
| 16–54 | Keeps only enabled workspaces, lists them predictably, and looks them up by name. |
| 55–91 | Rejects writes to read-only workspaces, absolute paths, `..`, protected locations, and resolved paths outside the workspace. |
| 92–114 | Resolves existing symlinks while still allowing a nonexistent final path for file creation. |
| 115–119 | Confirms a resolved path is below the configured root. |
| 120–137 | Blocks writes to Git internals, environment files, keys, certificates, and configured protected areas. |
| 138–147 | Blocks reads of common credential and secret filenames. |

### `internal/workspace/workspace_test.go` — 39 lines

Lines 1–28 test normal paths, `../`, absolute paths, and symlink escapes.
Lines 29–39 test disabled workspaces and the small boolean helper.

## File operations

### `internal/files/files.go` — 285 lines

| Lines | Meaning |
|---:|---|
| 1–37 | Imports helpers and defines bounded read/change response objects. |
| 38–89 | Reads only regular, non-binary, size-bounded files; selects line ranges and returns a SHA-256. |
| 90–133 | Creates files or safely overwrites only when the caller supplies the current SHA-256; writes atomically. |
| 134–170 | Applies one exact, unique text replacement and rejects stale hashes or ambiguous matches. |
| 171–199 | Deletes only a regular file whose current SHA-256 matches the caller's value. |
| 200–231 | Writes through a temporary file, flushes it, then renames it atomically so partial files are not exposed. |
| 232–278 | Calculates hashes and produces a small capped human-readable diff. |
| 279–285 | Copies at most the configured byte limit and reports truncation. |

### `internal/files/files_test.go` — 49 lines

Lines 1–19 create an isolated temporary service. Lines 20–33 test partial line
reads. Lines 34–49 test patch success and rejection of ambiguous patches.

## Search

### `internal/search/search.go` — 324 lines

| Lines | Meaning |
|---:|---|
| 1–37 | Imports helpers and defines compact tree/search result records. |
| 38–114 | Walks a bounded tree, skips dependency/Git/hidden/protected paths, and reports truncation. |
| 115–164 | Finds filenames by substring or glob with the same workspace protections. |
| 165–185 | Validates search inputs and prefers ripgrep when installed. |
| 186–252 | Runs ripgrep without a shell, with timeout, JSON output, result limit, and protected-path filtering. |
| 253–307 | Provides a slower Go fallback using a root-scoped file handle to prevent symlink escape while opening matches. |
| 308–315 | Converts absolute internal paths back to safe workspace-relative paths. |
| 316–324 | Applies default and administrator-set result limits. |

### `internal/search/search_test.go` — 22 lines

All lines build a temporary workspace and prove that search stops at the
configured result limit.

## Controlled commands

### `internal/command/command.go` — 200 lines

| Lines | Meaning |
|---:|---|
| 1–20 | Imports process, timing, JSON, synchronization, and workspace helpers. |
| 21–38 | Defines the runner settings and complete command result returned to the client. |
| 39–105 | Validates the working directory, clamps timeout, executes configured argv without a shell, limits output, kills on timeout, and records exit/duration details. |
| 106–144 | Detects Go, npm, Make, .NET, and CMake projects without inventing destructive setup commands. |
| 145–167 | Gives child processes a minimal environment and safely inspects npm scripts. |
| 168–200 | Implements a thread-safe buffer that accepts process output but stores only the configured maximum. |

`internal/command/process_unix.go` has 16 lines: it starts a separate process
group and terminates the whole group on timeout.

`internal/command/process_windows.go` has 11 lines: it supplies the Windows
equivalent build implementation.

`internal/command/command_test.go` has 38 lines: lines 1–15 build a temporary
runner, 16–28 prove timeout enforcement, and 29–38 prove output truncation.

## Git

### `internal/git/git.go` — 140 lines

| Lines | Meaning |
|---:|---|
| 1–20 | Allows conservative remote/branch characters and rejects values beginning with option-like `-` or force-like `+`. |
| 21–41 | Defines the service plus status and path-scoped diff. |
| 42–65 | Caps log history and implements fetch/prune with validated remotes. |
| 66–82 | Implements only fast-forward pulls. |
| 83–98 | Stages targeted validated paths; staging all with `.` is deliberately rejected. |
| 99–108 | Requires a nonempty, bounded commit message. |
| 109–127 | Pushes normally or sets upstream; no force option exists. |
| 128–140 | Sends Git argv through the same bounded command runner. |

`internal/git/git_test.go` has 16 lines and tests safe versus unsafe ref names.

## OpenAI client and API

### `internal/openai/client.go` — 135 lines

| Lines | Meaning |
|---:|---|
| 1–13 | Imports HTTP, JSON, context, byte, string, and time helpers. |
| 14–47 | Fixes the official Responses endpoint and defines private client state, public result fields, usage, and sanitized upstream errors. |
| 48–61 | Builds a timeout-enabled client; tests can substitute a fake endpoint. |
| 62–135 | Sends model/input with `store: false`, server-side bearer auth, and a request ID; caps response bodies, parses safe errors, extracts output text, and returns usage. |

`internal/openai/client_test.go` has 58 lines: lines 1–43 verify the outbound
request and parsed response; lines 44–58 verify safe upstream-error handling.

### `internal/mcp/openai.go` — 194 lines

| Lines | Meaning |
|---:|---|
| 1–19 | Imports HTTP, JSON, URL, synchronization, and OpenAI client helpers. |
| 20–44 | Implements a mutex-protected, one-minute global request limiter. |
| 45–73 | Serves the owner test page and JavaScript from the configured web root without caching. |
| 74–158 | Handles `OPTIONS /openai/prompt` and `POST /openai/prompt`: CORS, both credentials, content type, one JSON object, prompt size, upstream call, generic errors, and safe response. |
| 159–174 | Allows only exact configured browser origins or carefully checked wildcard origins. |
| 175–191 | Parses wildcard origins and prevents suffix tricks such as `example.com.evil.test`. |
| 192–194 | Logs timing/status only—not prompts or secrets. |

`internal/mcp/openai_test.go` has 94 lines: lines 1–40 build a fake upstream,
41–63 test authentication and response shape, 64–84 test good/bad CORS origins,
and 85–94 verify the strict browser Content Security Policy.

## MCP HTTP server

### `internal/mcp/server.go` — 687 lines

| Lines | Meaning |
|---:|---|
| 1–23 | Imports all small services used by the HTTP/MCP layer. |
| 24–36 | Declares version/protocol and the exact public download allowlist. |
| 37–75 | Defines server state, JSON-RPC request/response/error records, and MCP tool metadata. |
| 76–87 | Constructs workspace, file, search, command, Git, auth, OpenAI, and rate-limit services from validated config. |
| 88–103 | Registers exact routes; unknown paths are not treated as the homepage. |
| 104–118 | Adds browser security headers and a stricter script policy outside the test page. |
| 119–177 | Serves the homepage, root-scoped static assets, allowlisted downloads, and nonsensitive health JSON. |
| 178–231 | Enforces origin, method, body size, strict JSON-RPC shape, protocol headers, and handles initialize/ping/tool discovery. |
| 232–265 | Validates a tool call, dispatches it, logs only metadata, and returns bounded command details. |
| 266–524 | Implements every tool's small input schema, feature flag, validation, and call to the appropriate service. |
| 525–565 | Selects configured or safely detected build/test operations and rejects disabled command categories. |
| 566–644 | Writes JSON/RPC responses, checks origins/protocol headers, negotiates supported MCP revisions, and extracts safe log metadata. |
| 645–681 | Publishes the complete list of composable MCP tools and their JSON input schemas. |
| 682–687 | Resolves the default homepage path relative to the executable. |

`internal/mcp/server_test.go` has 95 lines: lines 1–30 build a test server,
31–60 test download allowlisting and exact routing, 61–79 test unauthorized
access, and 80–95 test authenticated initialization plus tool discovery.

## Web and operations files

| File | Plain-language purpose |
|---|---|
| `web/index.html` | Public operator manual and project homepage; contains no secret. |
| `web/launch-banner.svg` | Static banner shown on the homepage. |
| `web/openai-test.html` | Owner-only manual test form; it does not contain credentials. |
| `web/openai-test.js` | Keeps manually entered credentials in page memory and sends one request. Never prefill or bundle a production secret here. |
| `config.example.yaml` | Generic, secret-free configuration example. |
| `docs/mcp.service` | Hardened systemd service template. |
| `docs/nginx-mcp.conf` | HTTPS reverse-proxy template used by the live deployment. |
| `docs/certbot.example.sh` | Minimal example for adding one certificate without replacing an existing certificate workflow. |
| `scripts/deploy.sh` | Test, cross-build downloads, atomically install, restart, health-check, and rollback-on-failure workflow. |
| `scripts/check-mcp.sh` | Small authenticated MCP connectivity check; reads the token from environment or root-owned env file. |
| `scripts/mcp-token` | Root-only token retrieval/rotation; rotation preserves all other env entries. |
| `Makefile` | Reproducible cross-platform build and checksum commands. |
| `go.mod` / `go.sum` | Pin the Go toolchain and YAML dependency checksums. |
| `.gitignore` | Prevents binaries, environment files, credentials, keys, and coverage output from being committed. |
| `.github/workflows/ci.yml` | Runs tests, race detector, vet, vulnerability scan, and security scan. Actions are pinned to immutable commits. |
| `.github/dependabot.yml` | Requests weekly Go-module and GitHub-Action update reviews. |
| `SECURITY.md` | Private vulnerability-reporting instructions and operator responsibilities. |

## What to read first when changing behavior

- Authentication problem: `internal/auth/`, then the matching handler.
- File escaped a workspace: `internal/workspace/`, then
  `internal/files/` or `internal/search/`.
- Build/test timeout: `internal/command/`.
- Git behavior: `internal/git/`.
- OpenAI prompt endpoint: `internal/mcp/openai.go`, then
  `internal/openai/client.go`.
- MCP tool or HTTP route: `internal/mcp/server.go`.

Always add or update the nearest `*_test.go` file, then run the four commands
listed in [the security review](SECURITY_REVIEW.md).
