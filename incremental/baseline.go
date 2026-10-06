// Package incremental implements baseline-based incremental collection:
// only files whose source changed since the baseline need to be
// re-collected, while unchanged files are reused from the baseline. The
// rebuilt result is structurally identical to a full rebuild from the
// same inputs.
package incremental

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/shikejing/coverage-report-engine-go/covermodel"
)

// ChangeSet classifies files relative to a baseline. All paths are
// canonical, module-relative slash paths ("b/b.go", "a.go").
type ChangeSet struct {
	Changed   []string
	Unchanged []string
	Deleted   []string
}

// Diff walks the source tree rooted at rootDir (the module root) and
// compares each non-test .go file's SHA-256 with the hashes recorded in
// base, which was collected for modulePath.
//
//   - files present on disk with the same hash as in base -> Unchanged
//   - files present on disk that are new or have a different hash -> Changed
//   - files recorded in base but absent from disk -> Deleted
func Diff(modulePath, rootDir string, base *covermodel.Coverage) (*ChangeSet, error) {
	modulePath = strings.TrimSuffix(modulePath, "/")
	cs := &ChangeSet{}

	// Index baseline files by module-relative path.
	type bf struct{ hash string }
	baseline := make(map[string]bf)
	for _, p := range base.Packages {
		pkgRel := strings.TrimPrefix(strings.TrimPrefix(p.ImportPath, modulePath), "/")
		for _, f := range p.Files {
			rel := f.Path
			if pkgRel != "" {
				rel = pkgRel + "/" + f.Path
			}
			baseline[rel] = bf{hash: f.Hash}
		}
	}

	walked := make(map[string]bool)
	err := filepath.WalkDir(rootDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			n := d.Name()
			if n == "vendor" || (strings.HasPrefix(n, ".") && n != ".") {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(rootDir, path)
		if err != nil {
			return err
		}
		rel = covermodel.CanonicalPath(rel)
		walked[rel] = true
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(src)
		hash := hex.EncodeToString(sum[:])
		if old, ok := baseline[rel]; ok && old.hash != "" && old.hash == hash {
			cs.Unchanged = append(cs.Unchanged, rel)
		} else {
			cs.Changed = append(cs.Changed, rel)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("incremental: diff walk failed: %w", err)
	}
	for rel := range baseline {
		if !walked[rel] {
			cs.Deleted = append(cs.Deleted, rel)
		}
	}
	sort.Strings(cs.Changed)
	sort.Strings(cs.Unchanged)
	sort.Strings(cs.Deleted)
	return cs, nil
}

// relToKey converts a module-relative file path into the
// (importPath, file name) pair used by the coverage model.
func relToKey(modulePath, rel string) (string, string) {
	dir, name := covermodel.SplitModulePath(rel)
	importPath := modulePath
	if dir != "." {
		if modulePath != "" {
			importPath += "/"
		}
		importPath += dir
	}
	return importPath, name
}

// Rebuild produces global coverage from a baseline plus freshly
// collected coverage for changed files:
//
//   - files in changes.Changed are taken from `changed` and replace any
//     baseline data (a changed file absent from `changed` loses its
//     baseline blocks, exactly as a full rebuild would show);
//   - files in changes.Unchanged are reused verbatim from base;
//   - files in changes.Deleted are dropped;
//   - run IDs of baseline and new runs are retained (reused statements
//     were genuinely covered by the earlier runs);
//   - missing ranges of the new run are carried forward.
//
// The returned coverage equals what merging a freshly collected full
// profile over the same sources would produce.
func Rebuild(modulePath string, base, changed *covermodel.Coverage, changes *ChangeSet) (*covermodel.Coverage, error) {
	if base == nil {
		if changed == nil {
			return &covermodel.Coverage{}, nil
		}
		return changed.Clone(), nil
	}
	if changed != nil && base.Mode != changed.Mode {
		return nil, fmt.Errorf("incremental: mode mismatch: baseline %q vs changed %q", base.Mode, changed.Mode)
	}
	out := &covermodel.Coverage{Mode: base.Mode}

	changedSet := make(map[string]bool, len(changes.Changed))
	for _, r := range changes.Changed {
		changedSet[r] = true
	}
	deletedSet := make(map[string]bool, len(changes.Deleted))
	for _, r := range changes.Deleted {
		deletedSet[r] = true
	}

	// 1) Copy baseline, skipping changed and deleted files.
	pkgIndex := make(map[string]*covermodel.Package)
	getPkg := func(importPath string) *covermodel.Package {
		p := pkgIndex[importPath]
		if p == nil {
			p = &covermodel.Package{ImportPath: importPath}
			pkgIndex[importPath] = p
			out.Packages = append(out.Packages, p)
		}
		return p
	}
	for _, p := range base.Packages {
		pkgRel := strings.TrimPrefix(strings.TrimPrefix(p.ImportPath, modulePath), "/")
		for _, f := range p.Files {
			rel := f.Path
			if pkgRel != "" {
				rel = pkgRel + "/" + f.Path
			}
			if changedSet[rel] || deletedSet[rel] {
				continue
			}
			dp := getPkg(p.ImportPath)
			dp.Files = append(dp.Files, cloneFile(f))
		}
		out.RunIDs = append(out.RunIDs, base.RunIDs...)
	}

	// 2) Overlay freshly collected files for every changed path.
	for _, rel := range changes.Changed {
		importPath, name := relToKey(modulePath, rel)
		var nf *covermodel.File
		if changed != nil {
			nf = changed.File(importPath, name)
		}
		dp := getPkg(importPath)
		if nf != nil {
			dp.Files = append(dp.Files, cloneFile(nf))
		} else {
			// Changed file produced no blocks in the new run (e.g. it no
			// longer contains executable statements). Record it with no
			// blocks so a later diff still sees it; a full rebuild would
			// likewise produce no blocks for it.
			dp.Files = append(dp.Files, &covermodel.File{Path: name})
		}
	}

	if changed != nil {
		out.RunIDs = append(out.RunIDs, changed.RunIDs...)
		out.Missing = append(out.Missing, changed.Missing...)
	}

	// Drop packages that became empty solely due to deletions.
	kept := out.Packages[:0]
	for _, p := range out.Packages {
		if len(p.Files) > 0 {
			kept = append(kept, p)
		}
	}
	out.Packages = kept
	out.Sort()
	return out, nil
}

func cloneFile(f *covermodel.File) *covermodel.File {
	return &covermodel.File{
		Path:   f.Path,
		Hash:   f.Hash,
		Blocks: append([]covermodel.Block(nil), f.Blocks...),
	}
}
