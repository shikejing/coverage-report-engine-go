package cover

import (
	"context"
	"fmt"
	"testing"
)

// fakeFS maps path -> (content, hit count) so hashes and profiles stay in sync.
type fakeFS struct {
	content map[string]string
	hits    map[string]int64
}

func (f *fakeFS) hashOf(p string) (string, error) {
	c, ok := f.content[p]
	if !ok {
		return "", fmt.Errorf("no such file %s", p)
	}
	h := 0
	for _, b := range []byte(c) {
		h = h*31 + int(b)
	}
	return fmt.Sprintf("%x", h), nil
}

func (f *fakeFS) profileFor(runID string, paths []string) *Profile {
	p := &Profile{Mode: ModeCount, RunID: runID}
	for _, path := range paths {
		hit := f.hits[path]
		p.Files = append(p.Files, &FileCoverage{
			Path:   path,
			Blocks: map[string]int64{"1:1:2:1:2": hit},
			Meta:   map[string]Block{"1:1:2:1:2": {StartLine: 1, StartCol: 1, EndLine: 2, EndCol: 1, NumStmt: 2}},
		})
	}
	return p
}

func TestIncrementalMatchesFullRebuild(t *testing.T) {
	fs := &fakeFS{
		content: map[string]string{"a.go": "v1", "b.go": "v1", "c/c.go": "v1"},
		hits:    map[string]int64{"a.go": 1, "b.go": 0, "c/c.go": 3},
	}
	paths := []string{"a.go", "b.go", "c/c.go"}

	// Baseline: full collection over v1.
	base := NewMerger()
	if _, err := base.Import(fs.profileFor("base", paths)); err != nil {
		t.Fatal(err)
	}
	for _, p := range paths {
		h, _ := fs.hashOf(p)
		base.SetContentHash(p, h)
	}

	// Change b.go: content and hits change.
	fs.content["b.go"] = "v2"
	fs.hits["b.go"] = 5

	inc := NewIncremental(base)
	changed, unchanged, err := inc.ChangedFiles(paths, fs.hashOf)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprintf("%v", changed) != "[b.go]" {
		t.Fatalf("changed = %v", changed)
	}
	if len(unchanged) != 2 {
		t.Fatalf("unchanged = %v", unchanged)
	}

	// Incremental: collect only changed files, reuse baseline for the rest.
	incMerger, err := inc.Build(context.Background(), []*Profile{fs.profileFor("inc-1", changed)}, fs.hashOf)
	if err != nil {
		t.Fatal(err)
	}

	// Full rebuild: collect everything fresh.
	full := NewMerger()
	if _, err := full.Import(fs.profileFor("full-1", paths)); err != nil {
		t.Fatal(err)
	}

	ri := incMerger.Result(nil)
	rf := full.Result(nil)
	if ri.Summary != rf.Summary {
		t.Fatalf("incremental %+v != full %+v", ri.Summary, rf.Summary)
	}
	for _, p := range paths {
		fi := FileStatOf(incMerger.File(p))
		ff := FileStatOf(full.File(p))
		if fi.Statements != ff.Statements || fi.Covered != ff.Covered {
			t.Fatalf("file %s: inc %+v != full %+v", p, fi, ff)
		}
	}
}

func TestIncrementalColdStart(t *testing.T) {
	fs := &fakeFS{
		content: map[string]string{"a.go": "v1"},
		hits:    map[string]int64{"a.go": 2},
	}
	inc := NewIncremental(nil)
	changed, unchanged, err := inc.ChangedFiles([]string{"a.go"}, fs.hashOf)
	if err != nil {
		t.Fatal(err)
	}
	if len(changed) != 1 || len(unchanged) != 0 {
		t.Fatalf("cold start: changed=%v unchanged=%v", changed, unchanged)
	}
	m, err := inc.Build(context.Background(), []*Profile{fs.profileFor("r1", changed)}, fs.hashOf)
	if err != nil {
		t.Fatal(err)
	}
	if m.ContentHash("a.go") == "" {
		t.Fatal("hash not recorded")
	}
}
