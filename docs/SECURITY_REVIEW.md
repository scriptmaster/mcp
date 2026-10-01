# Security review

Review date: 2026-10-01

Release reviewed: 1.1.1

## Plain-language result

No real secret is stored in this repository, all tests pass, and the automated
scanners report no known reachable Go vulnerability or unreviewed code-security
finding. The release pins Go 1.26.8 because the previous 1.26.0 build had known
standard-library vulnerabilities.

That is strong evidence, not a promise that bugs are impossible. This service
is powerful by design, so a stolen credential or unsafe administrator
configuration can still cause damage.

## What was checked

```bash
go test -race -count=1 ./...
go vet ./...
govulncheck ./...
gosec ./...
```

Results at publication:

- All unit, integration, and race tests passed.
- `go vet` passed.
- `govulncheck` reported: `No vulnerabilities found.`
- `gosec` reviewed 12 Go source files and reported zero issues. Seven
  deliberately annotated calls use paths or command arrays that are validated
  earlier by trusted configuration or `workspace.Manager.Resolve`.
- A redacted secret-pattern scan found no upstream provider key, GitHub token, AWS access
  key, private key, MCP bearer token, or DevSpectra API secret.

GitHub Actions reruns the first four checks on pushes, pull requests, and every
Monday. Dependabot checks Go modules and GitHub Actions weekly.

## Important protections

Authentication:

- `POST /mcp` requires a bearer token.
- `POST /openai/prompt` requires both `X-API-Key` and `X-API-Secret`.
- Comparisons are constant-time after SHA-256 normalization.
- Real secrets live in the root-owned `/etc/mcp/mcp.env`, not source code.

Files and paths:

- Clients select a configured workspace name, never an arbitrary server root.
- Absolute paths, `../` escapes, symlink escapes, secret-like filenames, Git
  internals, and operator-configured protected paths are rejected.
- Reads, writes, patches, searches, and command output have size limits.
- Overwrites and deletes require the current file SHA-256, preventing an older
  client from silently replacing newer content.
- Public web assets and downloads use Go's root-scoped filesystem API; download
  names also come from a fixed allowlist.

Commands and Git:

- There is no general shell-string endpoint.
- An administrator defines allowed commands as argument arrays.
- Commands run inside a validated workspace with a minimal environment,
  timeout, process-group termination, and output limits.
- Force push, hard reset, history rewriting, and branch deletion are absent.
- Push and deploy are separate explicit tools.

AI provider:

- The selected OpenAI or Gemini key stays server-side and is sent only
  to the matching fixed HTTPS API endpoint.
- OpenAI requests set `store: false`; both providers enforce
  prompt/output/time/rate limits and never log prompt text, provider keys, or client credentials.
- Upstream error details are not returned to the browser.
- The provider and model are configurable; defaults are `gpt-6-luna` for
  OpenAI and `gemini-flash-latest` for Gemini.
- OpenAI's [authentication guidance](https://developers.openai.com/api/reference/overview#authentication)
  says API keys belong in a server environment or key manager, not browser
  code. This project follows that rule.

Deployment:

- NGINX terminates HTTPS and proxies only to a localhost listener.
- systemd runs the binary as an unprivileged `mcp` user with filesystem,
  capability, device, home-directory, and kernel hardening.
- The deploy script tests and validates a temporary binary, performs an atomic
  install, restarts systemd, checks service state and HTTPS health, and retains
  a rollback binary until health succeeds.
- Token rotation now changes only `MCP_TOKEN`; it preserves provider and other
  environment entries.

## Risks the operator must still manage

1. **A valid credential is powerful.** Anyone with the MCP bearer token can use
   every enabled MCP capability. Anyone with the DevSpectra client pair can spend
   the selected provider budget within configured limits. Rotate credentials
   after suspected exposure.
2. **Do not embed the shared API secret in a shipped frontend.** Browser users
   can inspect JavaScript, headers, and network requests. The included test page
   stores credentials in that browser’s local storage after a successful request
   and is only for an owner on a trusted device. Production frontends should
   call their own backend, which then calls `POST /openai/prompt`.
3. **Configured commands are trusted code.** A dangerous build or deploy
   command can bypass application-level intent. Only root should edit the
   config, and command lists should be reviewed like shell scripts.
4. **Workspace access is real access.** Use a dedicated OS account and grant it
   only the repositories it needs. Do not run this service as root.
5. **Wildcard browser origins enlarge the trust boundary.** If
   `https://*.app.example.com` is allowed, every controlled subdomain becomes
   eligible to make browser requests when it also has the client credentials.
   Prefer an exact origin list where practical.
6. **The in-memory rate limiter is basic.** It is global to one process, resets
   on restart, and is not a billing quota. Set provider project budgets, quotas,
   and usage alerts as a second control.
7. **Local hostile-process races are outside the primary threat model.** The
   resolver checks symlinks and public assets use root-scoped APIs, but a
   separate malicious local process with write access to the same repository
   can still race normal filesystem and build operations. OS permissions are
   the main protection.

## Safe publication rule

Before every public release, rerun the checks above and a redacted secret scan.
Never add `/etc/mcp/mcp.env`, compiled production state, private repositories,
certificate keys, SSH keys, or a browser bundle containing shared credentials.
