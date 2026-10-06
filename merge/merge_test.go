package merge

import (
	"testing"

	"github.com/shikejing/coverage-report-engine-go/covermodel"
)

func makeCov(mode covermodel.Mode, counts ...int64) *covermodel.Coverage {
	var blocks []covermodel.Block
	for i, n := range counts {
		blocks = append(blocks, covermodel.Block{
			StartLine: i + 1, StartCol: 1, EndLine: i + 1, EndCol: 2,
			NumStmt: 1, Count: n,
		})
	}
	return &covermodel.Coverage{
		Mode:     mode,
		Packages: []*covermodel.Package{{ImportPath: "p", Files: []*covermodel.File{{Path: "f.go", Blocks: blocks}}}},
	}
}

func TestCountAccumulates(t *testing.T) {
	a := makeCov(covermodel.ModeCount, 1, 0, 3)
	b := makeCov(covermodel.ModeCount, 2, 5, 0)
	got, err := Combine(covermodel.ModeCount, map[string]*covermodel.Coverage{"a": a, "b": b})
	if err != nil {
		t.Fatal(err)
	}
	blocks := got.Package("p").Files[0].Blocks
	want := []int64{3, 5, 3}
	for i := range want {
		if blocks[i].Count != want[i] {
			t.Fatalf("block %d = %d want %d", i, blocks[i].Count, want[i])
		}
	}
}

func TestSetModeOR(t *testing.T) {
	a := makeCov(covermodel.ModeSet, 0, 1)
	b := makeCov(covermodel.ModeSet, 1, 0)
	got, err := Combine(covermodel.ModeSet, map[string]*covermodel.Coverage{"a": a, "b": b})
	if err != nil {
		t.Fatal(err)
	}
	for i, blk := range got.Package("p").Files[0].Blocks {
		if blk.Count != 1 {
			t.Fatalf("block %d = %d want 1 (OR)", i, blk.Count)
		}
	}
}

func TestDuplicateRunIDNotDoubleCounted(t *testing.T) {
	m := New(covermodel.ModeCount)
	acc := &covermodel.Coverage{Mode: covermodel.ModeCount}
	a := makeCov(covermodel.ModeCount, 4)
	if added, err := m.Merge(acc, a, "same"); err != nil || !added {
		t.Fatalf("first merge added=%v err=%v", added, err)
	}
	if added, err := m.Merge(acc, a, "same"); err != nil || added {
		t.Fatalf("second identical merge added=%v err=%v, want false/nil", added, err)
	}
	blocks := acc.Package("p").Files[0].Blocks
	if len(blocks) != 1 || blocks[0].Count != 4 {
		t.Fatalf("duplicate import double counted: %+v", blocks)
	}
}

func TestOrderIndependence(t *testing.T) {
	mk := func(id string, counts ...int64) *covermodel.Coverage {
		c := makeCov(covermodel.ModeCount, counts...)
		c.RunIDs = []string{id}
		return c
	}
	r1 := mk("z", 1, 2)
	r2 := mk("a", 3, 4)
	out1, err := Combine(covermodel.ModeCount, map[string]*covermodel.Coverage{"z": r1, "a": r2})
	if err != nil {
		t.Fatal(err)
	}
	r1b := mk("z", 1, 2)
	r2b := mk("a", 3, 4)
	out2, err := Combine(covermodel.ModeCount, map[string]*covermodel.Coverage{"a": r2b, "z": r1b})
	if err != nil {
		t.Fatal(err)
	}
	b1 := out1.Package("p").Files[0].Blocks
	b2 := out2.Package("p").Files[0].Blocks
	for i := range b1 {
		if b1[i].Count != b2[i].Count {
			t.Fatalf("order dependence at block %d: %d vs %d", i, b1[i].Count, b2[i].Count)
		}
	}
	if out1.RunIDs[0] != "a" || out1.RunIDs[1] != "z" {
		t.Fatalf("run ids not sorted: %v", out1.RunIDs)
	}
}

func TestModeMismatchRejected(t *testing.T) {
	a := makeCov(covermodel.ModeCount, 1)
	b := makeCov(covermodel.ModeSet, 1)
	if _, err := Combine(covermodel.ModeCount, map[string]*covermodel.Coverage{"a": a, "b": b}); err == nil {
		t.Fatal("expected mode mismatch error")
	}
}

func TestRunIDDeterministic(t *testing.T) {
	data := []byte("mode: count\np/f.go:1.1,1.2 1 3\n")
	id1 := RunID(data)
	id2 := RunID(data)
	id3 := RunID(append([]byte{}, data...))
	if id1 != id2 || id2 != id3 {
		t.Fatalf("RunID not deterministic: %s %s %s", id1, id2, id3)
	}
	if RunID([]byte("different")) == id1 {
		t.Fatal("RunID collided on different content")
	}
}

func TestMissingRangesUnioned(t *testing.T) {
	a := makeCov(covermodel.ModeCount, 1)
	a.Missing = []covermodel.MissingRange{{Shard: "s1", Kind: "failed"}}
	b := makeCov(covermodel.ModeCount, 2)
	b.Missing = []covermodel.MissingRange{{Shard: "s2", Kind: "timeout"}}
	got, err := Combine(covermodel.ModeCount, map[string]*covermodel.Coverage{"a": a, "b": b})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Missing) != 2 || got.Missing[0].Shard != "s1" {
		t.Fatalf("missing ranges not unioned/sorted: %+v", got.Missing)
	}
}
