package cover

import (
	"context"
	"sort"
)

// Incremental collects coverage only for files whose content hash changed
// since the baseline, reusing stored coverage for unchanged files, while
// still reporting global totals. For identical inputs its result equals a
// full rebuild: unchanged files contribute exactly the baseline entries a
// fresh run would reproduce.
type Incremental struct {
	baseline *Merger
}

// NewIncremental creates an incremental collector on top of a baseline
// store (may be nil for a cold start).
func NewIncremental(baseline *Merger) *Incremental {
	return &Incremental{baseline: baseline}
}

// ChangedFiles partitions paths into changed/unchanged relative to the
// baseline content hashes. Files without a baseline hash are changed.
// Both output slices are sorted for determinism.
func (in *Incremental) ChangedFiles(paths []string, hashOf func(string) (string, error)) (changed, unchanged []string, err error) {
	for _, p := range paths {
		h, herr := hashOf(p)
		if herr != nil {
			return nil, nil, herr
		}
		if in.baseline != nil && in.baseline.ContentHash(p) == h && in.baseline.File(p) != nil {
			unchanged = append(unchanged, p)
		} else {
			changed = append(changed, p)
		}
	}
	sort.Strings(changed)
	sort.Strings(unchanged)
	return changed, unchanged, nil
}

// Build merges freshly collected profiles for changed files with baseline
// coverage for unchanged files, and records current content hashes.
// Unchanged baseline entries are imported under a synthetic run ID so
// they merge exactly once; fresh profiles keep their own RunIDs.
func (in *Incremental) Build(ctx context.Context, fresh []*Profile, hashOf func(string) (string, error)) (*Merger, error) {
	m := NewMerger()

	// Reuse baseline coverage for files not touched by fresh profiles.
	freshPaths := map[string]bool{}
	for _, p := range fresh {
		for _, f := range p.Files {
			freshPaths[f.Path] = true
		}
	}
	if in.baseline != nil {
		reused := &Profile{Mode: in.baseline.mode, RunID: "__baseline__"}
		for _, f := range in.baseline.Files() {
			if !freshPaths[f.Path] {
				reused.Files = append(reused.Files, f)
			}
		}
		if len(reused.Files) > 0 {
			if _, err := m.Import(reused); err != nil {
				return nil, err
			}
		}
	}

	for _, p := range fresh {
		if err := ctx.Err(); err != nil {
			return nil, reject(ReasonCanceled, "incremental build canceled: %v", err)
		}
		if _, err := m.Import(p); err != nil {
			return nil, err
		}
	}

	// Record current hashes for every file we now know about.
	for _, f := range m.Files() {
		h, err := hashOf(f.Path)
		if err != nil {
			return nil, err
		}
		m.SetContentHash(f.Path, h)
	}
	return m, nil
}
