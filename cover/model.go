// Package cover implements a local test-coverage collection, merge and
// reporting engine. It consumes coverage profiles produced by local test
// runs (Go coverprofile text format), merges results across runs and
// shards, supports incremental collection against a baseline, enforces
// coverage thresholds and emits deterministic reports.
package cover

// Block is a single executable coverage block within a source file.
type Block struct {
	StartLine int `json:"startLine"`
	StartCol  int `json:"startCol"`
	EndLine   int `json:"endLine"`
	EndCol    int `json:"endCol"`
	NumStmt   int `json:"numStmt"`
}

// key returns a canonical identity string for the block, used for merging.
func (b Block) key() string {
	return itoa(b.StartLine) + ":" + itoa(b.StartCol) + ":" + itoa(b.EndLine) + ":" + itoa(b.EndCol) + ":" + itoa(b.NumStmt)
}

// FileCoverage holds the hit counts of every block of one source file.
type FileCoverage struct {
	Path   string           `json:"path"`
	Blocks map[string]int64 `json:"blocks"` // block key -> hit count
	Meta   map[string]Block `json:"meta"`   // block key -> block geometry
}

// FileStat is the aggregated coverage of one file.
type FileStat struct {
	Path         string  `json:"path"`
	Statements   int     `json:"statements"`
	Covered      int     `json:"covered"`
	Percent      float64 `json:"percent"`
	ContentHash  string  `json:"contentHash,omitempty"`
	FromBaseline bool    `json:"fromBaseline,omitempty"`
}

// PackageStat is the aggregated coverage of one package (directory).
type PackageStat struct {
	Path       string     `json:"path"`
	Statements int        `json:"statements"`
	Covered    int        `json:"covered"`
	Percent    float64    `json:"percent"`
	Files      []FileStat `json:"files,omitempty"`
}

// MissingRange marks a deterministic file range whose coverage is unknown
// because a shard failed or timed out. Missing ranges are never counted
// as zero-hit.
type MissingRange struct {
	Path      string `json:"path"`
	StartLine int    `json:"startLine"`
	EndLine   int    `json:"endLine"`
	Reason    string `json:"reason"`
}

// Summary is the global coverage summary.
type Summary struct {
	Statements int     `json:"statements"`
	Covered    int     `json:"covered"`
	Percent    float64 `json:"percent"`
	Complete   bool    `json:"complete"` // false when any shard is missing
}

// Result is the outcome of a collection/merge pass.
type Result struct {
	Summary  Summary        `json:"summary"`
	Packages []PackageStat  `json:"packages"`
	Missing  []MissingRange `json:"missing,omitempty"`
}

// itoa is a tiny allocation-free-ish int formatter to avoid strconv in hot paths.
func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
