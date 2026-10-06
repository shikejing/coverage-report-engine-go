package collect

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/shikejing/coverage-report-engine-go/covermodel"
)

// Collector validates profiles against the local source tree. Profiles
// referencing files that are missing from the tree, or carrying malformed
// ranges, are rejected rather than silently counted as uncovered.
type Collector struct {
	// ModulePath is the module import path prefix.
	ModulePath string
	// RootDir is the local module root used to verify source files.
	RootDir string
	// Mode optionally pins the expected mode. A zero value accepts the
	// mode declared by the first collected profile.
	Mode covermodel.Mode
}

// New constructs a Collector.
func New(modulePath, rootDir string, mode covermodel.Mode) *Collector {
	return &Collector{
		ModulePath: strings.TrimSuffix(strings.TrimSpace(modulePath), "/"),
		RootDir:    rootDir,
		Mode:       mode,
	}
}

// CollectFile reads a profile from path and collects it. A missing or
// unreadable file yields a *RejectError of KindMissing.
func (c *Collector) CollectFile(path, runID string) (*covermodel.Coverage, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, &RejectError{
			Kind:   KindMissing,
			Source: path,
			Reason: "coverage profile is missing or unreadable",
			Err:    err,
		}
	}
	return c.Collect(data, path, runID)
}

// Collect parses and validates one profile run against the local source
// tree. sourceName identifies the input in error messages; runID is the
// deterministic identifier used later for duplicate suppression.
func (c *Collector) Collect(data []byte, sourceName, runID string) (*covermodel.Coverage, error) {
	parsed, err := ParseProfile(data, sourceName)
	if err != nil {
		return nil, err
	}
	if c.Mode != "" && parsed.Mode != c.Mode {
		return nil, &RejectError{
			Kind:   KindModeMismatch,
			Source: sourceName,
			Reason: fmt.Sprintf("profile mode %q does not match required mode %q", parsed.Mode, c.Mode),
		}
	}
	c.Mode = parsed.Mode

	// Aggregate repeated block positions the same way `go tool cover`
	// does: set -> any hit, count/atomic -> summed hits.
	type aggFile struct {
		blocks []covermodel.Block
	}
	files := make(map[string]*aggFile)
	var order []string
	for _, rec := range parsed.Records {
		af := files[rec.File]
		if af == nil {
			af = &aggFile{}
			files[rec.File] = af
			order = append(order, rec.File)
		}
		af.blocks = append(af.blocks, rec.Block)
	}

	cov := &covermodel.Coverage{Mode: parsed.Mode}
	if runID != "" {
		cov.RunIDs = []string{runID}
	}
	pkgs := make(map[string]*covermodel.Package)

	for _, importFile := range order {
		rel, ok := c.moduleRelative(importFile)
		if !ok {
			return nil, &RejectError{
				Kind:   KindUnknownSource,
				Source: sourceName,
				Reason: fmt.Sprintf("block references %q which is outside module %q", importFile, c.ModulePath),
			}
		}
		diskPath := filepath.Join(c.RootDir, filepath.FromSlash(rel))
		src, err := os.ReadFile(diskPath)
		if err != nil {
			return nil, &RejectError{
				Kind:   KindUnknownSource,
				Source: sourceName,
				Reason: fmt.Sprintf("source file for %q not found in local module: %s", importFile, diskPath),
				Err:    err,
			}
		}
		hashBytes := sha256.Sum256(src)
		hash := hex.EncodeToString(hashBytes[:])
		nlines := bytes.Count(src, []byte("\n")) + 1

		blocks := files[importFile].blocks
		for _, b := range blocks {
			if b.EndLine > nlines || b.StartLine > nlines {
				return nil, &RejectError{
					Kind:   KindCorrupt,
					Source: sourceName,
					Reason: fmt.Sprintf("block in %q extends beyond source file (%d lines)", importFile, nlines),
				}
			}
		}
		merged, err := mergeBlocks(parsed.Mode, blocks)
		if err != nil {
			return nil, &RejectError{Kind: KindCorrupt, Source: sourceName, Err: err}
		}

		dir, name := covermodel.SplitModulePath(rel)
		importPath := c.importPathOf(dir)
		pkg := pkgs[importPath]
		if pkg == nil {
			pkg = &covermodel.Package{ImportPath: importPath}
			pkgs[importPath] = pkg
			cov.Packages = append(cov.Packages, pkg)
		}
		pkg.Files = append(pkg.Files, &covermodel.File{
			Path:   name,
			Hash:   hash,
			Blocks: merged,
		})
	}

	cov.Sort()
	return cov, nil
}

// moduleRelative converts an import-qualified profile path into a path
// relative to the module root.
func (c *Collector) moduleRelative(importFile string) (string, bool) {
	rel := covermodel.CanonicalPath(importFile)
	if c.ModulePath != "" {
		if rel == c.ModulePath || !strings.HasPrefix(rel, c.ModulePath+"/") {
			return "", false
		}
		rel = strings.TrimPrefix(rel, c.ModulePath+"/")
	}
	if rel == "" || strings.HasPrefix(rel, "../") || filepath.IsAbs(filepath.FromSlash(rel)) {
		return "", false
	}
	return rel, true
}

// importPathOf returns the full import path of a module-relative package
// directory ("." denotes the module root).
func (c *Collector) importPathOf(dir string) string {
	if dir == "." {
		return c.ModulePath
	}
	if c.ModulePath == "" {
		return dir
	}
	return c.ModulePath + "/" + dir
}

// mergeBlocks combines blocks that share identical source ranges.
func mergeBlocks(mode covermodel.Mode, blocks []covermodel.Block) ([]covermodel.Block, error) {
	index := make(map[covermodel.Block]int)
	var out []covermodel.Block
	for _, b := range blocks {
		key := b
		key.Count = 0
		if i, ok := index[key]; ok {
			switch mode {
			case covermodel.ModeSet:
				if b.Count > 0 || out[i].Count > 0 {
					out[i].Count = 1
				}
			default: // count and atomic
				out[i].Count += b.Count
			}
			continue
		}
		index[key] = len(out)
		out = append(out, b)
	}
	// Restore deterministic source order.
	sortBlocks(out)
	return out, nil
}

// sortBlocks sorts blocks by source position.
func sortBlocks(blocks []covermodel.Block) {
	for i := 1; i < len(blocks); i++ {
		for j := i; j > 0; j-- {
			a, b := blocks[j-1], blocks[j]
			less := a.StartLine < b.StartLine ||
				(a.StartLine == b.StartLine && a.StartCol < b.StartCol) ||
				(a.StartLine == b.StartLine && a.StartCol == b.StartCol && a.EndLine < b.EndLine) ||
				(a.StartLine == b.StartLine && a.StartCol == b.StartCol && a.EndLine == b.EndLine && a.EndCol < b.EndCol)
			if !less {
				break
			}
			blocks[j-1], blocks[j] = blocks[j], blocks[j-1]
		}
	}
}
