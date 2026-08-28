# Pre-v1 API and release policy

This document is the release gate for the first tagged version of
`github.com/alvnukov/cozy-tools` (review task CT-008). It freezes what the
module exports, states the compatibility policy, and records the release
verification matrix and migration procedure.

## Package map and stability classes

The README package table is the authoritative package map. Stability classes:

### Stable surface (v0.x compatibility applies)

These are the packages downstream hosts (mcp-ai-helper, later cozyphi) build
against. Breaking changes to exported identifiers require a minor-version
bump and a release-notes entry:

| Package | Stable entry points |
| --- | --- |
| `tool`, `tooltest` | `Tool`, `Spec`, `Result`, effects catalog, contract-test harness |
| `fstools` | tool catalogs over the file family |
| `fileops` | guarded edits, search, file state (host-facing API) |
| `gitops` | status/log/diff/commit plumbing, `CommitOwned` transaction |
| `command` | durable runner, history, wait/abort, output policy, redaction |
| `config` | `CommandPolicy`, `ResolvedValues`, `ValueResolver`, `DefaultConfigPath` seam |
| `security` | `Mask` with named handles |
| `evidence` | failure-line distillation, citation validation |
| `project` | per-repository storage paths |
| `vars` | fail-closed literal substitution and argv/env/stdin channels |
| `testhome` | test-binary HOME isolation |

### Internal mechanism (frozen semantics, not stable surface)

`safefs` and `filesystem` are the rooted mechanism and the policy layer of
the file family. Their exported APIs are usable but intentionally narrow;
their semantics (containment, atomic replacement via
`safefs.Root.WriteFileAtomicOpts`, limits, error classification) are frozen:
divergence between the layers is a bug (see `filesystem/doc.go`).

## Compatibility policy (pre-v1)

- `v0.x` follows Go module semantics: tagged versions are immutable; the
  module path never changes within v0.
- Within v0, additive changes are patch/minor bumps; breaking changes to
  the stable surface require a minor bump (0.N to 0.N+1) and a release-notes
  migration section.
- Overlapping write APIs across the file family
  (`safefs.Root.WriteFileAtomic`, `filesystem.Service.Write`,
  `fileops.WriteFile`) share the atomic-replacement semantics of
  `safefs.Root.WriteFileAtomicOpts`; consolidation or deprecation is decided
  at v1 planning, not incrementally.
- No host-specific (mcp-ai-helper- or cozyphi-only) identifier is part of
  the stable surface; host adaptation lives in the `config` seam.

## Platform support

Supported build targets: darwin/amd64, darwin/arm64, linux/amd64,
linux/arm64, windows/amd64, freebsd/amd64, openbsd/amd64 (Go 1.26+).

Known contract difference: `gitops` guarded-commit cross-process locking uses
flock(2) on unix; on windows it degrades to intra-process serialization
(documented on the lock seam in `gitops/lock_windows.go`). Transactional
`CommitOwned` guarantees across separate processes are unix-only.

## Release verification matrix

Run on a clean worktree before tagging:

| Gate | Command | Notes |
| --- | --- |
| Format | `gofmt -l .` | empty output |
| Vet | `go vet ./...` | |
| Tests | `go test ./... -count=1` | |
| Race | `go test ./... -count=1 -race` | |
| Workspace diagnostics | `gopls check` on changed files | zero diagnostics |
| Cross-build | `go build ./...` per supported GOOS/GOARCH | all seven targets |
| Downstream consumption | temp module requiring cozy-tools without replace | builds and tests |

`staticcheck` is not part of the matrix: it does not build against Go 1.26
toolchains (tooling issue recorded in the review, not a code defect).

## Release notes and migration (replace removal)

The first tag (`v0.1.0`) freezes this policy. Downstream migration from the
local replace:

1. cozy-tools: tag the reviewed commit `v0.1.0` and push branch + tag.
2. mcp-ai-helper: replace the `replace github.com/alvnukov/cozy-tools =>
   /Users/zol/src/cozy-tools` directive with `require
   github.com/alvnukov/cozy-tools v0.1.0`, then `go mod tidy`.
3. Verify `go build ./...` and the full mcp-ai-helper test suite against the
   tag.
4. cozyphi integration remains intentionally deferred until its refactoring
   lands (tracked by the review); this module is designed so cozyphi can adopt
   file/command families without API changes on the stable surface.
