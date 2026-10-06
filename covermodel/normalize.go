package covermodel

import (
	"path"
	"sort"
	"strings"
)

// CanonicalPath converts a source path into a cleaned slash-separated
// path. Only separators and redundant elements are normalized.
func CanonicalPath(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	p = path.Clean(p)
	if p == "." {
		return ""
	}
	return strings.TrimPrefix(p, "./")
}

// blockLess gives the canonical source order of blocks.
func blockLess(a, b Block) bool {
	if a.StartLine != b.StartLine {
		return a.StartLine < b.StartLine
	}
	if a.StartCol != b.StartCol {
		return a.StartCol < b.StartCol
	}
	if a.EndLine != b.EndLine {
		return a.EndLine < b.EndLine
	}
	return a.EndCol < b.EndCol
}

// Sort orders packages, files, blocks, run IDs and missing ranges
// deterministically. Exact duplicate block positions are deduplicated in
// place, keeping the larger hit count.
func (c *Coverage) Sort() {
	c.RunIDs = dedupSortedStrings(c.RunIDs)
	sort.SliceStable(c.Missing, func(i, j int) bool {
		if c.Missing[i].Shard != c.Missing[j].Shard {
			return c.Missing[i].Shard < c.Missing[j].Shard
		}
		return c.Missing[i].Kind < c.Missing[j].Kind
	})
	sort.SliceStable(c.Packages, func(i, j int) bool {
		return c.Packages[i].ImportPath < c.Packages[j].ImportPath
	})
	for _, pkg := range c.Packages {
		sort.SliceStable(pkg.Files, func(i, j int) bool {
			return pkg.Files[i].Path < pkg.Files[j].Path
		})
		for _, f := range pkg.Files {
			sort.SliceStable(f.Blocks, func(i, j int) bool {
				return blockLess(f.Blocks[i], f.Blocks[j])
			})
			if len(f.Blocks) > 1 {
				uniq := f.Blocks[:1]
				for _, b := range f.Blocks[1:] {
					last := &uniq[len(uniq)-1]
					if blockPosEqual(*last, b) {
						if b.Count > last.Count {
							last.Count = b.Count
						}
						continue
					}
					uniq = append(uniq, b)
				}
				f.Blocks = uniq
			}
		}
	}
}

// dedupSortedStrings sorts s, removes duplicates and returns it.
func dedupSortedStrings(s []string) []string {
	sort.Strings(s)
	if len(s) < 2 {
		return s
	}
	out := s[:1]
	for _, v := range s[1:] {
		if v != out[len(out)-1] {
			out = append(out, v)
		}
	}
	return out
}

// SplitModulePath splits a canonical module-relative source path into the
// package directory path relative to the module root ("." for the root
// package) and the file path relative to that package directory.
//
// Examples:
//
//	"a.go"         -> (".", "a.go")
//	"foo/bar.go"   -> ("foo", "bar.go")
//	"foo/sub/x.go" -> ("foo/sub", "x.go")
func SplitModulePath(rel string) (pkgPath, name string) {
	rel = CanonicalPath(rel)
	if rel == "" {
		return ".", ""
	}
	dir := path.Dir(rel)
	if dir == "." || dir == "/" {
		return ".", path.Base(rel)
	}
	return dir, path.Base(rel)
}

// blockPosEqual compares block identity (position and statement count),
// ignoring hit counts.
func blockPosEqual(a, b Block) bool {
	return a.StartLine == b.StartLine && a.StartCol == b.StartCol &&
		a.EndLine == b.EndLine && a.EndCol == b.EndCol && a.NumStmt == b.NumStmt
}
