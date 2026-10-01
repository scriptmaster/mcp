# Security policy

## Supported version

Security fixes are applied to the current `main` branch.

## Reporting a vulnerability

Please do not publish an exploitable vulnerability in a normal GitHub issue.
Use this repository's **Security → Report a vulnerability** option so the
details remain private while the problem is investigated.

Include the affected route or tool, a minimal reproduction, expected behavior,
actual behavior, and any suggested mitigation. Never include real bearer
tokens, OpenAI keys, API secrets, private repository content, or user data.

## Operator responsibility

This server intentionally performs high-impact repository operations. Operators
must use a dedicated unprivileged service account, expose only intended
workspaces, protect `/etc/mcp/mcp.env`, keep write/push/deploy actions
approval-gated, and install security updates promptly.
