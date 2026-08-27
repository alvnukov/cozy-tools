# cozy-tools

Shared Go library of agent tool implementations for [mcp-ai-helper](https://github.com/alvnukov/mcp-ai-helper) and cozyphi.

## Scope

cozy-tools holds the tool families that exist in **both** hosts:

- **file family** — read, write, edit, grep, ls, find
- **command family** — shell execution (bash)

plus the git plumbing and the leaf packages those families need (secret
masking, evidence distillation, project-scoped storage, template variables,
policy data). Families that only one host has (jira, confluence, web, model,
notes, tasks, workflows, agents, plan, lsp, ...) stay in their host.

## Packages

| Package | Role |
| --- | --- |
| `tool`, `tooltest` | neutral Tool/Spec/Result/effects catalog contract and contract-test harness |
| `filesystem` | typed rooted filesystem service (read/list/find/search/write/edit, atomic writes, gitignore semantics, limits) |
| `fstools` | native and MCP tool catalogs over the file family |
| `fileops`, `safefs` | guarded edits, search, gitignore, file state (ported verbatim from mcp-ai-helper) |
| `command` | durable command runner: coordinator, history, wait/abort, output policy, redaction |
| `config` | host-injectable seam: CommandPolicy data, ResolvedValues, ValueResolver, DefaultConfigPath |
| `gitops` | status/log/diff/commit plumbing behind the git tools |
| `security` | secret masking (Mask with named handles) |
| `evidence` | failure-line distillation and citation validation |
| `project` | per-repository storage paths (hash-stable project names) |
| `vars` | fail-closed literal template substitution |
| `testhome` | test-binary HOME isolation |

Dependency direction: hosts import cozy-tools; cozy-tools never imports a
host. Host-specific configuration crosses through the `config` seam
(CommandPolicy as data, ValueResolver implemented by the host) — see
mcp-ai-helper's `internal/cozybridge` for the reference adapter.

## Integration status

- mcp-ai-helper: wired for file/edit (fileops, safefs), git (gitops), project,
  and the full command family (command, evidence, vars) via cozybridge.
- cozyphi: pending its current refactoring; readtool/writetool/grep/ls/find
  move next.

## Development

During migration mcp-ai-helper pins cozy-tools with a local replace
directive; remove the replace (and require a tagged version) before release.
Run `go test ./...` in this repository as the gate for library changes.
