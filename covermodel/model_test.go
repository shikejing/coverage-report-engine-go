package covermodel

import "testing"

func TestModeValid(t *testing.T) {
	for _, m := range []Mode{ModeSet, ModeCount, ModeAtomic} {
		if !m.Valid() {
			t.Fatalf("mode %s should be valid", m)
		}
	}
	if Mode("bogus").Valid() {
		t.Fatal("bogus mode must be invalid")
	}
}

func TestPercent(t *testing.T) {
	c := &Coverage{
		Mode: ModeCount,
		Packages: []*Package{{
			ImportPath: "x",
			Files: []*File{{
				Path: "a.go",
				Blocks: []Block{
					{NumStmt: 2, Count: 1},
					{NumStmt: 3, Count: 0},
					{NumStmt: 5, Count: 4},
				},
			}},
		}},
	}
	if got := c.TotalStatements(); got != 10 {
		t.Fatalf("total = %d want 10", got)
	}
	if got := c.CoveredStatements(); got != 7 {
		t.Fatalf("covered = %d want 7", got)
	}
	if got := c.Percent(); got != 70 {
		t.Fatalf("percent = %v want 70", got)
	}
}

func TestSortAndDedup(t *testing.T) {
	c := &Coverage{
		Mode:   ModeCount,
		RunIDs: []string{"b", "a", "a"},
		Packages: []*Package{
			{ImportPath: "z", Files: []*File{{Path: "z.go"}}},
			{ImportPath: "a", Files: []*File{
				{Path: "b.go", Blocks: []Block{{StartLine: 2, NumStmt: 1, Count: 3}, {StartLine: 1, NumStmt: 1, Count: 1}}},
				{Path: "a.go", Blocks: []Block{{StartLine: 1, NumStmt: 1, Count: 5}, {StartLine: 1, NumStmt: 1, Count: 2}}},
			}},
		},
	}
	c.Sort()
	if c.RunIDs[0] != "a" || len(c.RunIDs) != 2 {
		t.Fatalf("run ids not deduped/sorted: %v", c.RunIDs)
	}
	if c.Packages[0].ImportPath != "a" {
		t.Fatalf("packages not sorted: %s", c.Packages[0].ImportPath)
	}
	af := c.Package("a").Files[0]
	if af.Path != "a.go" || af.Blocks[0].Count != 5 {
		t.Fatalf("duplicate block not collapsed to max count: %+v", af.Blocks[0])
	}
	if c.Package("a").Files[1].Path != "b.go" || c.Package("a").Files[1].Blocks[0].StartLine != 1 {
		t.Fatalf("blocks not sorted by source position: %+v", c.Package("a").Files[1])
	}
}

func TestSplitModulePath(t *testing.T) {
	cases := map[string][2]string{
		"a.go":         {".", "a.go"},
		"foo/bar.go":   {"foo", "bar.go"},
		"foo/sub/x.go": {"foo/sub", "x.go"},
	}
	for in, want := range cases {
		dir, name := SplitModulePath(in)
		if dir != want[0] || name != want[1] {
			t.Fatalf("SplitModulePath(%q) = (%q,%q) want (%q,%q)", in, dir, name, want[0], want[1])
		}
	}
}

func TestClone(t *testing.T) {
	c := &Coverage{Mode: ModeSet, Packages: []*Package{{ImportPath: "p", Files: []*File{{Path: "f", Blocks: []Block{{NumStmt: 1}}}}}}}
	d := c.Clone()
	d.Packages[0].Files[0].Blocks[0].Count = 9
	if c.Packages[0].Files[0].Blocks[0].Count != 0 {
		t.Fatal("clone shares block storage")
	}
}
