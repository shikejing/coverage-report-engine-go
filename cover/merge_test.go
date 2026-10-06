package cover

import (
	"strings"
	"testing"
)

func prof(t *testing.T, runID, body string) *Profile {
	t.Helper()
	p, err := ParseProfile(strings.NewReader(body), ".", runID)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

const runA = `mode: count
pkg/a.go:1.1,2.1 1 1
pkg/a.go:3.1,4.1 2 0
pkg/sub/b.go:1.1,5.1 3 4
`

const runB = `mode: count
pkg/a.go:1.1,2.1 1 2
pkg/a.go:3.1,4.1 2 1
pkg/sub/b.go:1.1,5.1 3 0
`

func TestMergeAccumulatesHits(t *testing.T) {
	m := NewMerger()
	if ok, err := m.Import(prof(t, "a", runA)); !ok || err != nil {
		t.Fatalf("import a: %v %v", ok, err)
	}
	if ok, err := m.Import(prof(t, "b", runB)); !ok || err != nil {
		t.Fatalf("import b: %v %v", ok, err)
	}
	f := m.File("pkg/a.go")
	if got := f.Blocks["1:1:2:1:1"]; got != 3 {
		t.Fatalf("block hits = %d, want 3", got)
	}
	if got := f.Blocks["3:1:4:1:2"]; got != 1 {
		t.Fatalf("block hits = %d, want 1", got)
	}
}

func TestMergeDuplicateRunIDNoDoubleCount(t *testing.T) {
	m := NewMerger()
	ok1, _ := m.Import(prof(t, "a", runA))
	ok2, _ := m.Import(prof(t, "a", runA))
	if !ok1 || ok2 {
		t.Fatalf("imported flags = %v,%v", ok1, ok2)
	}
	if got := m.File("pkg/a.go").Blocks["1:1:2:1:1"]; got != 1 {
		t.Fatalf("hits = %d, want 1 (no double count)", got)
	}
}

func TestMergeOrderIndependent(t *testing.T) {
	build := func(order []string) *Result {
		m := NewMerger()
		for _, id := range order {
			body := runA
			if id == "b" {
				body = runB
			}
			if _, err := m.Import(prof(t, id, body)); err != nil {
				t.Fatal(err)
			}
		}
		return m.Result(nil)
	}
	r1 := build([]string{"a", "b"})
	r2 := build([]string{"b", "a"})
	if r1.Summary != r2.Summary {
		t.Fatalf("summary differs: %+v vs %+v", r1.Summary, r2.Summary)
	}
	if len(r1.Packages) != len(r2.Packages) {
		t.Fatal("package count differs")
	}
	for i := range r1.Packages {
		if r1.Packages[i].Path != r2.Packages[i].Path || r1.Packages[i].Percent != r2.Packages[i].Percent {
			t.Fatalf("package %d differs", i)
		}
	}
}

func TestMergeModeMismatchRejected(t *testing.T) {
	m := NewMerger()
	if _, err := m.Import(prof(t, "a", runA)); err != nil {
		t.Fatal(err)
	}
	other := prof(t, "c", "mode: set\npkg/a.go:1.1,2.1 1 1\n")
	if _, err := m.Import(other); err == nil {
		t.Fatal("want mode mismatch rejection")
	}
}

func TestResultAggregation(t *testing.T) {
	m := NewMerger()
	m.Import(prof(t, "a", runA))
	res := m.Result(nil)
	// statements: 1+2+3 = 6, covered: 1+3 = 4
	if res.Summary.Statements != 6 || res.Summary.Covered != 4 {
		t.Fatalf("summary = %+v", res.Summary)
	}
	if !res.Summary.Complete {
		t.Fatal("should be complete")
	}
	if len(res.Packages) != 2 || res.Packages[0].Path != "pkg" || res.Packages[1].Path != "pkg/sub" {
		t.Fatalf("packages = %+v", res.Packages)
	}
}
