package filesystem

import "io/fs"

const (
	defaultWholeFileBytes = int64(8 << 20)
	defaultReadLines      = 1000
	defaultReadBytes      = int64(50 << 10)
	defaultListEntries    = 500
	defaultFindMatches    = 100
	defaultSearchMatches  = 100
	defaultSearchBytes    = int64(50 << 10)
	defaultScanFiles      = 10_000
	defaultScanBytes      = int64(64 << 20)
)

// Limits contains service-wide safety caps. Zero fields passed to WithLimits
// retain their safe defaults. Positive values may lower, but may not exceed,
// the defaults returned by DefaultLimits.
type Limits struct {
	WholeFileBytes int64 `json:"whole_file_bytes"`
	ReadLines      int   `json:"read_lines"`
	ReadBytes      int64 `json:"read_bytes"`
	ListEntries    int   `json:"list_entries"`
	FindMatches    int   `json:"find_matches"`
	SearchMatches  int   `json:"search_matches"`
	SearchBytes    int64 `json:"search_bytes"`
	ScanFiles      int   `json:"scan_files"`
	ScanBytes      int64 `json:"scan_bytes"`
}

// DefaultLimits returns the package's safe default caps.
func DefaultLimits() Limits {
	return Limits{
		WholeFileBytes: defaultWholeFileBytes,
		ReadLines:      defaultReadLines,
		ReadBytes:      defaultReadBytes,
		ListEntries:    defaultListEntries,
		FindMatches:    defaultFindMatches,
		SearchMatches:  defaultSearchMatches,
		SearchBytes:    defaultSearchBytes,
		ScanFiles:      defaultScanFiles,
		ScanBytes:      defaultScanBytes,
	}
}

// Option configures a Service during Open.
type Option func(*openConfig) error

// WithLimits lowers service-wide limits. A value above a safe default is
// rejected; use zero to leave that field unchanged.
func WithLimits(limits Limits) Option {
	return func(config *openConfig) error {
		merged, err := mergeLimits(config.limits, limits)
		if err != nil {
			return err
		}
		config.limits = merged
		return nil
	}
}

// Truncation explains why a bounded result stopped and how to continue.
type Truncation struct {
	Truncated     bool   `json:"truncated"`
	Reason        string `json:"reason,omitempty"`
	Continuation  string `json:"continuation,omitempty"`
	ReturnedItems int    `json:"returned_items,omitempty"`
	ReturnedBytes int64  `json:"returned_bytes,omitempty"`
	ScannedFiles  int    `json:"scanned_files,omitempty"`
	ScannedBytes  int64  `json:"scanned_bytes,omitempty"`
}

// FileMetadata describes a root-relative filesystem object.
type FileMetadata struct {
	Path    string      `json:"path"`
	Name    string      `json:"name,omitempty"`
	Type    string      `json:"type"`
	Size    int64       `json:"size,omitempty"`
	Mode    fs.FileMode `json:"mode"`
	ModTime string      `json:"mod_time,omitempty"`
}

// Line is one LF-normalized text line with its stable content anchor.
type Line struct {
	Number int    `json:"number"`
	Anchor string `json:"anchor"`
	Text   string `json:"text"`
}

// ReadRequest selects a bounded, 1-based line range. Offset defaults to 1;
// Limit and MaxBytes default to their configured service caps.
type ReadRequest struct {
	Path     string `json:"path"`
	Offset   int    `json:"offset,omitempty"`
	Limit    int    `json:"limit,omitempty"`
	MaxBytes int64  `json:"max_bytes,omitempty"`
}

// ReadResult is a raw-hash-aware, text-or-binary file snapshot.
type ReadResult struct {
	File         FileMetadata `json:"file"`
	SHA256       string       `json:"sha256"`
	HashChunks   []string     `json:"hash_chunks"`
	Binary       bool         `json:"binary"`
	Content      string       `json:"content,omitempty"`
	Data         []byte       `json:"data,omitempty"`
	Lines        []Line       `json:"lines"`
	TotalLines   int          `json:"total_lines"`
	Offset       int          `json:"offset"`
	NextOffset   int          `json:"next_offset,omitempty"`
	Newline      string       `json:"newline,omitempty"`
	FinalNewline bool         `json:"final_newline,omitempty"`
	Truncation   Truncation   `json:"truncation"`
}

// ListRequest controls recursive listing. MaxDepth defaults to 3; a value of
// one includes direct children only.
type ListRequest struct {
	Path       string `json:"path,omitempty"`
	MaxDepth   int    `json:"max_depth,omitempty"`
	ShowHidden bool   `json:"show_hidden,omitempty"`
	MaxEntries int    `json:"max_entries,omitempty"`
	ScanFiles  int    `json:"scan_files,omitempty"`
	ScanBytes  int64  `json:"scan_bytes,omitempty"`
}

// Entry is an ASCII-tree-ready list or find item.
type Entry struct {
	Path    string      `json:"path"`
	Name    string      `json:"name"`
	Type    string      `json:"type"`
	Depth   int         `json:"depth"`
	Size    int64       `json:"size,omitempty"`
	Mode    fs.FileMode `json:"mode"`
	Ignored bool        `json:"ignored,omitempty"`
}

// ListResult contains deterministic entries and a pre-rendered ASCII tree.
type ListResult struct {
	Root       string     `json:"root"`
	Entries    []Entry    `json:"entries"`
	Tree       string     `json:"tree"`
	Truncation Truncation `json:"truncation"`
}

// FindRequest finds paths using slash-separated glob syntax, including **.
type FindRequest struct {
	Path       string `json:"path,omitempty"`
	Pattern    string `json:"pattern"`
	ShowHidden bool   `json:"show_hidden,omitempty"`
	Limit      int    `json:"limit,omitempty"`
	ScanFiles  int    `json:"scan_files,omitempty"`
	ScanBytes  int64  `json:"scan_bytes,omitempty"`
}

// FindResult contains deterministic root-relative matches.
type FindResult struct {
	Paths      []string   `json:"paths"`
	Entries    []Entry    `json:"entries"`
	Truncation Truncation `json:"truncation"`
}

// SearchRequest performs a bounded line-oriented search.
type SearchRequest struct {
	Path            string   `json:"path,omitempty"`
	Pattern         string   `json:"pattern"`
	Literal         bool     `json:"literal,omitempty"`
	CaseInsensitive bool     `json:"case_insensitive,omitempty"`
	Glob            string   `json:"glob,omitempty"`
	Globs           []string `json:"globs,omitempty"`
	ShowHidden      bool     `json:"show_hidden,omitempty"`
	Before          int      `json:"before,omitempty"`
	After           int      `json:"after,omitempty"`
	Limit           int      `json:"limit,omitempty"`
	MaxBytes        int64    `json:"max_bytes,omitempty"`
	ScanFiles       int      `json:"scan_files,omitempty"`
	ScanBytes       int64    `json:"scan_bytes,omitempty"`
}

// ContextLine is a line adjacent to a search match.
type ContextLine struct {
	Number int    `json:"number"`
	Anchor string `json:"anchor"`
	Text   string `json:"text"`
}

// SearchMatch is one matching line and its requested context.
type SearchMatch struct {
	Path   string        `json:"path"`
	SHA256 string        `json:"sha256"`
	Line   int           `json:"line"`
	Anchor string        `json:"anchor"`
	Text   string        `json:"text"`
	Before []ContextLine `json:"before,omitempty"`
	After  []ContextLine `json:"after,omitempty"`
}

// SearchResult contains deterministic line matches and scan metadata.
type SearchResult struct {
	Matches      []SearchMatch `json:"matches"`
	SkippedFiles int           `json:"skipped_files,omitempty"`
	Truncation   Truncation    `json:"truncation"`
}

// WriteMode controls target existence behavior.
type WriteMode string

const (
	// WriteOverwrite atomically creates or replaces a file.
	WriteOverwrite WriteMode = "overwrite"
	// WriteCreateOnly atomically fails if the target already exists.
	WriteCreateOnly WriteMode = "create_only"
)

// WriteRequest contains raw bytes or text. Non-nil Data is written verbatim;
// otherwise Content is converted directly to bytes, including when empty.
type WriteRequest struct {
	Path           string    `json:"path"`
	Data           []byte    `json:"data,omitempty"`
	Content        string    `json:"content,omitempty"`
	Mode           WriteMode `json:"mode,omitempty"`
	CreateOnly     bool      `json:"create_only,omitempty"`
	ExpectedSHA256 string    `json:"expected_sha256,omitempty"`
	Permissions    *uint32   `json:"permissions,omitempty"`
}

// WriteResult describes an atomic write or idempotent no-op.
type WriteResult struct {
	Path      string `json:"path"`
	OldSHA256 string `json:"old_sha256,omitempty"`
	NewSHA256 string `json:"new_sha256"`
	Changed   bool   `json:"changed"`
	Status    string `json:"status"`
	Bytes     int64  `json:"bytes"`
	Mode      uint32 `json:"mode"`
}

// HashlineEdit replaces an inclusive line range identified by number#anchor.
// A nil Content deletes the range.
type HashlineEdit struct {
	From    string  `json:"from"`
	To      string  `json:"to"`
	Content *string `json:"content"`
}

// ReplaceEdit performs a unique old-text replacement.
type ReplaceEdit struct {
	Old string `json:"old"`
	New string `json:"new"`
}

// EditRequest selects either one unique replacement or one or more hashline
// ranges. Hashline edits require ExpectedSHA256.
type EditRequest struct {
	Path           string         `json:"path"`
	ExpectedSHA256 string         `json:"expected_sha256,omitempty"`
	Replace        *ReplaceEdit   `json:"replace,omitempty"`
	OldText        string         `json:"old_text,omitempty"`
	NewText        string         `json:"new_text,omitempty"`
	Edits          []HashlineEdit `json:"edits,omitempty"`
}

// EditResult describes the old and new snapshots.
type EditResult struct {
	Path      string `json:"path"`
	OldSHA256 string `json:"old_sha256"`
	NewSHA256 string `json:"new_sha256"`
	Changed   bool   `json:"changed"`
	Status    string `json:"status"`
}
