package cover

import (
	"sort"
	"strings"
)

// Merger accumulates profiles from many runs and shards into one
// canonical coverage database. Import is idempotent per RunID and the
// final result is independent of import order (hit counts accumulate by
// plain addition, which is commutative).
type Merger struct {
	mode   Mode
	hasRun bool
	runs   map[string]struct{}
	files  map[string]*FileCoverage
	hashes map[string]string // path -> content hash (optional)
}

// NewMerger creates an empty merger. The first imported profile fixes
// the counting mode; profiles of a different mode are rejected.
func NewMerger() *Merger {
	return &Merger{
		runs:   map[string]struct{}{},
		files:  map[string]*FileCoverage{},
		hashes: map[string]string{},
	}
}

// Runs returns the sorted IDs of imported runs.
func (m *Merger) Runs() []string {
	out := make([]string, 0, len(m.runs))
	for id := range m.runs {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// Import merges one profile. Re-importing the same RunID is a no-op and
// reports false, so duplicate imports never double-count.
func (m *Merger) Import(p *Profile) (bool, error) {
	if p == nil {
		return false, reject(ReasonCorruptPayload, "nil profile")
	}
	if p.RunID != "" {
		if _, dup := m.runs[p.RunID]; dup {
			return false, nil
		}
	}
	if m.hasRun && m.mode != p.Mode {
		return false, reject(ReasonUnknownMode, "mode mismatch: merger holds %q, profile is %q", m.mode, p.Mode)
	}
	m.mode = p.Mode
	m.hasRun = true
	if p.RunID != "" {
		m.runs[p.RunID] = struct{}{}
	}
	for _, f := range p.Files {
		fc := m.files[f.Path]
		if fc == nil {
			fc = &FileCoverage{Path: f.Path, Blocks: map[string]int64{}, Meta: map[string]Block{}}
			m.files[f.Path] = fc
		}
		for k, b := range f.Meta {
			if prev, ok := fc.Meta[k]; ok && prev != b {
				return false, reject(ReasonDupBlockGeom, "file %q block %s has conflicting geometry across runs", f.Path, k)
			}
			fc.Meta[k] = b
		}
		for k, c := range f.Blocks {
			fc.Blocks[k] += c
		}
	}
	return true, nil
}

// SetContentHash records the content hash of a source file.
func (m *Merger) SetContentHash(path, hash string) {
	m.hashes[path] = hash
}

// ContentHash returns the recorded hash of a source file.
func (m *Merger) ContentHash(path string) string {
	return m.hashes[path]
}

// Hashes returns a copy of all recorded content hashes.
func (m *Merger) Hashes() map[string]string {
	out := make(map[string]string, len(m.hashes))
	for k, v := range m.hashes {
		out[k] = v
	}
	return out
}

// Files returns the merged per-file coverage, sorted by path.
func (m *Merger) Files() []*FileCoverage {
	out := make([]*FileCoverage, 0, len(m.files))
	for _, f := range m.files {
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// File returns the merged coverage of one path, or nil.
func (m *Merger) File(path string) *FileCoverage {
	return m.files[path]
}

// FileStat computes the aggregate statistics of one merged file.
func FileStatOf(fc *FileCoverage) FileStat {
	st := FileStat{Path: fc.Path}
	for k, b := range fc.Meta {
		st.Statements += b.NumStmt
		if fc.Blocks[k] > 0 {
			st.Covered += b.NumStmt
		}
	}
	st.Percent = percent(st.Covered, st.Statements)
	return st
}

// Result computes aggregated statistics over the merged data. Missing
// ranges (from failed shards) are reported but never counted as zero-hit;
// when any are present the summary is marked incomplete.
func (m *Merger) Result(missing []MissingRange) *Result {
	res := &Result{Summary: Summary{Complete: len(missing) == 0}, Missing: missing}
	byPkg := map[string]*PackageStat{}
	var pkgOrder []string
	for _, fc := range m.Files() {
		fs := FileStatOf(fc)
		fs.ContentHash = m.hashes[fc.Path]
		pkg := packageOf(fc.Path)
		ps := byPkg[pkg]
		if ps == nil {
			ps = &PackageStat{Path: pkg}
			byPkg[pkg] = ps
			pkgOrder = append(pkgOrder, pkg)
		}
		ps.Statements += fs.Statements
		ps.Covered += fs.Covered
		ps.Files = append(ps.Files, fs)
		res.Summary.Statements += fs.Statements
		res.Summary.Covered += fs.Covered
	}
	sort.Strings(pkgOrder)
	for _, pkg := range pkgOrder {
		ps := byPkg[pkg]
		ps.Percent = percent(ps.Covered, ps.Statements)
		res.Packages = append(res.Packages, *ps)
	}
	res.Summary.Percent = percent(res.Summary.Covered, res.Summary.Statements)
	return res
}

// packageOf maps a file path to its package (directory) path.
func packageOf(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[:i]
	}
	return "."
}

// percent computes a stable percentage rounded to 2 decimals so output
// is reproducible across runs and platforms.
func percent(covered, total int) float64 {
	if total == 0 {
		return 100.0
	}
	return float64(int(float64(covered)/float64(total)*10000+0.5)) / 100
}
