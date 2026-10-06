// Package merge combines coverage runs deterministically.
package merge

import (
	"fmt"
	"sort"

	"github.com/shikejing/coverage-report-engine-go/covermodel"
)

// Merger accumulates runs while rejecting double counting. The same
// result is produced regardless of the order in which runs are merged.
type Merger struct {
	mode    covermodel.Mode
	runs    map[string]struct{}
	current *covermodel.Coverage
}

// New returns a Merger for the given mode.
func New(mode covermodel.Mode) *Merger {
	return &Merger{
		mode:    mode,
		runs:    make(map[string]struct{}),
		current: &covermodel.Coverage{Mode: mode},
	}
}

// Mode returns the counter mode the merger was configured with.
func (m *Merger) Mode() covermodel.Mode { return m.mode }

// Merge incorporates src under runID. A runID already incorporated is
// skipped, so re-importing the same result never double counts. It
// reports whether the run was newly incorporated.
func (m *Merger) Merge(dst *covermodel.Coverage, src *covermodel.Coverage, runID string) (bool, error) {
	if src == nil {
		return false, fmt.Errorf("merge: source coverage is nil")
	}
	if src.Mode != m.mode {
		return false, &ModeMismatchError{Wanted: m.mode, Got: src.Mode}
	}
	if runID != "" {
		if _, seen := m.runs[runID]; seen {
			return false, nil
		}
	}
	mergeInto(dst, src, m.mode)
	if runID != "" {
		m.runs[runID] = struct{}{}
		dst.RunIDs = append(dst.RunIDs, runID)
	}
	dst.Sort()
	return true, nil
}

// Add is the streaming variant: it merges into the merger's internal
// accumulator and reports whether runID was new.
func (m *Merger) Add(src *covermodel.Coverage, runID string) (bool, error) {
	return m.Merge(m.current, src, runID)
}

// Result returns a sorted deep copy of the accumulated coverage.
func (m *Merger) Result() *covermodel.Coverage {
	m.current.Sort()
	return m.current.Clone()
}

// ModeMismatchError indicates runs with incompatible counter modes were
// merged.
type ModeMismatchError struct {
	Wanted, Got covermodel.Mode
}

func (e *ModeMismatchError) Error() string {
	return fmt.Sprintf("merge: mode mismatch: want %q got %q", e.Wanted, e.Got)
}

// mergeInto merges src into dst in place. Block ranges are matched by
// source position and statement count; set mode ORs hits while
// count/atomic modes sum them.
func mergeInto(dst, src *covermodel.Coverage, mode covermodel.Mode) {
	for _, sp := range src.Packages {
		dp := dst.Package(sp.ImportPath)
		if dp == nil {
			dp = &covermodel.Package{ImportPath: sp.ImportPath}
			dst.Packages = append(dst.Packages, dp)
		}
		for _, sf := range sp.Files {
			df := fileOf(dp, sf.Path)
			if df == nil {
				df = &covermodel.File{Path: sf.Path, Hash: sf.Hash}
				dp.Files = append(dp.Files, df)
			} else if df.Hash == "" && sf.Hash != "" {
				df.Hash = sf.Hash
			}
			mergeFileBlocks(df, sf, mode)
		}
	}
	// Missing ranges are unioned so a failed shard in either run remains
	// visible in the merged result.
	dst.Missing = append(dst.Missing, src.Missing...)
}

func fileOf(pkg *covermodel.Package, path string) *covermodel.File {
	for _, f := range pkg.Files {
		if f.Path == path {
			return f
		}
	}
	return nil
}

type blockKey struct {
	startLine, startCol, endLine, endCol, numStmt int
}

func keyOf(b covermodel.Block) blockKey {
	return blockKey{b.StartLine, b.StartCol, b.EndLine, b.EndCol, b.NumStmt}
}

func mergeFileBlocks(df *covermodel.File, sf *covermodel.File, mode covermodel.Mode) {
	index := make(map[blockKey]int, len(df.Blocks))
	for i, b := range df.Blocks {
		index[keyOf(b)] = i
	}
	for _, sb := range sf.Blocks {
		k := keyOf(sb)
		if i, ok := index[k]; ok {
			switch mode {
			case covermodel.ModeSet:
				if sb.Count > 0 || df.Blocks[i].Count > 0 {
					df.Blocks[i].Count = 1
				}
			default:
				df.Blocks[i].Count += sb.Count
			}
			continue
		}
		index[k] = len(df.Blocks)
		df.Blocks = append(df.Blocks, sb)
	}
}

// Combine merges several named runs in one deterministic pass. Runs are
// applied in lexicographic runID order, so the final result is
// independent of the map iteration order of the input. Duplicate
// identifiers cannot occur in a map; callers importing twice must use a
// Merger with the same runID for idempotent skip semantics.
func Combine(mode covermodel.Mode, runs map[string]*covermodel.Coverage) (*covermodel.Coverage, error) {
	ids := make([]string, 0, len(runs))
	for id := range runs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	merger := New(mode)
	acc := &covermodel.Coverage{Mode: mode}
	for _, id := range ids {
		rc := runs[id]
		if rc.Mode != mode {
			return nil, &ModeMismatchError{Wanted: mode, Got: rc.Mode}
		}
		if _, err := merger.Merge(acc, rc, id); err != nil {
			return nil, err
		}
	}
	acc.Sort()
	return acc, nil
}
