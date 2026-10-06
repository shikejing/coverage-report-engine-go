// Package covermodel defines the core coverage data model shared by all
// collection, merge, storage and reporting components.
package covermodel

// Mode is the coverage counter mode, mirroring the modes emitted by the
// standard Go toolchain: set, count and atomic.
type Mode string

// Supported coverage modes.
const (
	ModeSet    Mode = "set"
	ModeCount  Mode = "count"
	ModeAtomic Mode = "atomic"
)

// Block is one executable source range and its hit count.
type Block struct {
	StartLine int
	StartCol  int
	EndLine   int
	EndCol    int
	NumStmt   int
	Count     int64
}

// File holds the collected blocks for a single source file.
type File struct {
	// Path is the file path relative to its package directory, using slash
	// separators on every platform (e.g. "a.go" or "sub/b.go").
	Path string
	// Hash is the hex SHA-256 of the source contents. It is empty for
	// storage format v1 data which could not record hashes.
	Hash string
	// Blocks are stored in source order.
	Blocks []Block
}

// Package groups the files belonging to one package import path.
type Package struct {
	ImportPath string
	Files      []*File
}

// MissingRange describes a shard or run whose coverage could not be
// collected. Ranges are recorded instead of silently counting zero.
type MissingRange struct {
	// Shard is the deterministic identifier of the failed shard/run.
	Shard string
	// Kind is one of "failed", "timeout" or "canceled".
	Kind string
	// Detail carries a human readable, non-secret explanation.
	Detail string
}

// Coverage is the merged result of zero or more test runs.
type Coverage struct {
	Mode     Mode
	Packages []*Package
	// RunIDs is the sorted, deduplicated list of run identifiers whose
	// results have been incorporated.
	RunIDs []string
	// Missing is the sorted list of ranges known to be uncollected.
	Missing []MissingRange
}

// Valid reports whether m is a supported mode.
func (m Mode) Valid() bool {
	switch m {
	case ModeSet, ModeCount, ModeAtomic:
		return true
	default:
		return false
	}
}

// TotalStatements returns the number of executable statements.
func (c *Coverage) TotalStatements() int {
	n := 0
	for _, pkg := range c.Packages {
		for _, f := range pkg.Files {
			for _, b := range f.Blocks {
				n += b.NumStmt
			}
		}
	}
	return n
}

// CoveredStatements returns the number of executed statements. A
// statement counts as covered when its block was hit at least once.
func (c *Coverage) CoveredStatements() int {
	n := 0
	for _, pkg := range c.Packages {
		for _, f := range pkg.Files {
			for _, b := range f.Blocks {
				if b.Count > 0 {
					n += b.NumStmt
				}
			}
		}
	}
	return n
}

// Percent returns the statement coverage percentage in the range 0..100.
// It returns 100 when there are no executable statements.
func (c *Coverage) Percent() float64 {
	total := c.TotalStatements()
	if total == 0 {
		return 100
	}
	return float64(c.CoveredStatements()) * 100 / float64(total)
}

// Package returns the named package or nil.
func (c *Coverage) Package(importPath string) *Package {
	for _, pkg := range c.Packages {
		if pkg.ImportPath == importPath {
			return pkg
		}
	}
	return nil
}

// File returns the named file within a package, or nil. relPath is the
// slash-separated path relative to the package directory.
func (c *Coverage) File(importPath, relPath string) *File {
	pkg := c.Package(importPath)
	if pkg == nil {
		return nil
	}
	for _, f := range pkg.Files {
		if f.Path == relPath {
			return f
		}
	}
	return nil
}

// Clone returns a deep copy so callers cannot mutate shared results.
func (c *Coverage) Clone() *Coverage {
	if c == nil {
		return nil
	}
	out := &Coverage{Mode: c.Mode}
	out.RunIDs = append(out.RunIDs, c.RunIDs...)
	out.Missing = append(out.Missing, c.Missing...)
	for _, pkg := range c.Packages {
		np := &Package{ImportPath: pkg.ImportPath}
		for _, f := range pkg.Files {
			nf := &File{Path: f.Path, Hash: f.Hash}
			nf.Blocks = append(nf.Blocks, f.Blocks...)
			np.Files = append(np.Files, nf)
		}
		out.Packages = append(out.Packages, np)
	}
	return out
}
