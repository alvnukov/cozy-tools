// Package fstools exposes native and MCP filesystem tool catalogs backed by
// the typed filesystem package.
//
// Native renderers identify snapshots with an eight-character uppercase TAG.
// A TAG is the leading eight hexadecimal characters of the full raw SHA-256;
// both a copied TAG and the full hash are accepted by edit operations.
package fstools
