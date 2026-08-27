// Package filesystem provides bounded, root-confined filesystem operations.
//
// A Service uses [os.Root] for every path lookup. Callers must supply paths
// relative to the service root; absolute paths and parent-directory escapes are
// rejected before they reach the operating system.
package filesystem
