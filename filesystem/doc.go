// Package filesystem provides bounded, root-confined filesystem operations.
//
// A Service uses [os.Root] for every path lookup. Callers must supply paths
// relative to the service root; absolute paths and parent-directory escapes are
// rejected before they reach the operating system.
//
// # File-family seam map
//
// The file family has one rooted mechanism and layered consumers:
//
//   - safefs.Root owns path containment, the symlink policy of os.Root,
//     parent-directory creation, and atomic replacement
//     (WriteFileAtomic/WriteFileAtomicOpts). It is the authoritative
//     implementation of those semantics.
//   - filesystem.Service (this package) is the policy layer over a root:
//     size limits, ignore rules, search, walk, edit guards and error
//     classification. Its write installations delegate to safefs; it adds
//     no atomic-write logic of its own. The deliberate difference from raw
//     safefs is the Limit and error-code contract callers see.
//   - fileops is the host-facing guarded-edit API (hash guards, unique
//     spans, batched writes) built on safefs.Root.
//   - fstools is the MCP tool surface over fileops.
//
// # Compatibility policy (pre-v1)
//
// The overlapping exported write APIs (safefs.Root.WriteFileAtomic,
// filesystem.Service.Write, fileops.WriteFile) are frozen surface until the
// first stable version: they share the atomic-replacement semantics above,
// and any divergence is a bug. Consolidation or deprecation of overlapping
// entry points is decided by the release gate, not incrementally.
package filesystem
