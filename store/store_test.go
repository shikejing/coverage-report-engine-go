package store

import (
	"bytes"
	"strings"
	"testing"

	"github.com/shikejing/coverage-report-engine-go/covermodel"
)

func sample() *covermodel.Coverage {
	return &covermodel.Coverage{
		Mode:   covermodel.ModeCount,
		RunIDs: []string{"r2", "r1"},
		Missing: []covermodel.MissingRange{
			{Shard: "shard-b", Kind: "timeout", Detail: "deadline exceeded"},
		},
		Packages: []*covermodel.Package{
			{ImportPath: "example.com/p", Files: []*covermodel.File{
				{Path: "a.go", Hash: "h1", Blocks: []covermodel.Block{
					{StartLine: 1, StartCol: 1, EndLine: 1, EndCol: 2, NumStmt: 1, Count: 3},
					{StartLine: 2, StartCol: 1, EndLine: 2, EndCol: 2, NumStmt: 2, Count: 0},
				}},
			}},
			{ImportPath: "example.com/p/b", Files: []*covermodel.File{
				{Path: "b.go", Hash: "h2", Blocks: []covermodel.Block{
					{StartLine: 4, StartCol: 3, EndLine: 6, EndCol: 1, NumStmt: 1, Count: 7},
				}},
			}},
		},
	}
}

func TestRoundTripV2(t *testing.T) {
	var buf bytes.Buffer
	if err := Save(&buf, sample()); err != nil {
		t.Fatal(err)
	}
	got, err := Load(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if got.Mode != covermodel.ModeCount {
		t.Fatalf("mode = %s", got.Mode)
	}
	if f := got.File("example.com/p", "a.go"); f == nil || f.Hash != "h1" || len(f.Blocks) != 2 {
		t.Fatalf("file not restored: %+v", f)
	} else if f.Blocks[0].Count != 3 {
		t.Fatalf("count not restored: %d", f.Blocks[0].Count)
	}
	if f := got.File("example.com/p/b", "b.go"); f == nil || f.Hash != "h2" {
		t.Fatalf("nested file not restored: %+v", f)
	}
	if len(got.Missing) != 1 || got.Missing[0].Shard != "shard-b" || got.Missing[0].Detail != "deadline exceeded" {
		t.Fatalf("missing range not restored: %+v", got.Missing)
	}
	if got.RunIDs[0] != "r1" || got.RunIDs[1] != "r2" {
		t.Fatalf("runs not sorted: %v", got.RunIDs)
	}
}

func TestDeterministicBytes(t *testing.T) {
	var b1, b2 bytes.Buffer
	if err := Save(&b1, sample()); err != nil {
		t.Fatal(err)
	}
	if err := Save(&b2, sample()); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(b1.Bytes(), b2.Bytes()) {
		t.Fatal("same data produced different bytes")
	}
	if !strings.HasPrefix(b1.String(), "# coverage-engine v2\n") {
		t.Fatalf("missing v2 header:\n%s", b1.String())
	}
}

func TestPeekVersion(t *testing.T) {
	var buf bytes.Buffer
	_ = Save(&buf, sample())
	v, err := PeekVersion(buf.Bytes())
	if err != nil || v != CurrentVersion {
		t.Fatalf("peek = %d, %v", v, err)
	}
}

func TestLoadV1(t *testing.T) {
	// Simulate a v1 file: no hashes, old file records, no missing ranges.
	v1 := "# coverage-engine v1\n" +
		"mode: count\n" +
		"run:\tr1\n" +
		"file\t\"example.com/p/a.go\"\n" +
		"b\t1.1,1.2\t1\t3\n" +
		"b\t2.1,2.2\t2\t0\n"
	got, err := Load(strings.NewReader(v1))
	if err != nil {
		t.Fatal(err)
	}
	if got.Mode != covermodel.ModeCount {
		t.Fatalf("mode = %s", got.Mode)
	}
	if f := got.File("example.com/p", "a.go"); f == nil {
		t.Fatalf("v1 file not imported, packages: %+v", got.Packages)
	} else if f.Hash != "" {
		t.Fatalf("v1 file should have empty hash, got %q", f.Hash)
	} else if f.Blocks[0].Count != 3 {
		t.Fatalf("v1 counts wrong: %+v", f.Blocks)
	}
	// V1 data must still merge with current in-memory model.
	var mergedBuf bytes.Buffer
	if err := Save(&mergedBuf, got); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(mergedBuf.String(), "# coverage-engine v2\n") {
		t.Fatal("upgraded store should be rewritten as v2")
	}
}

func TestRejectFutureVersion(t *testing.T) {
	_, err := Load(strings.NewReader("# coverage-engine v99\nmode: count\n"))
	if err == nil {
		t.Fatal("future version must be rejected")
	}
	if ve, ok := err.(*VersionError); !ok {
		t.Fatalf("want *VersionError, got %T %v", err, err)
	} else if ve.Got != 99 {
		t.Fatalf("version = %d want 99", ve.Got)
	}
}

func TestRejectAncientVersion(t *testing.T) {
	_, err := Load(strings.NewReader("# coverage-engine v0\nmode: count\n"))
	if err == nil {
		t.Fatal("ancient version must be rejected")
	}
}

func TestRejectForeignFile(t *testing.T) {
	_, err := Load(strings.NewReader("mode: count\np/f.go:1.1,1.2 1 1\n"))
	if err == nil {
		t.Fatal("foreign coverprofile must be rejected by store load")
	}
	if _, ok := err.(*VersionError); !ok {
		t.Fatalf("want *VersionError got %T", err)
	}
}

func TestRejectCorruptV2(t *testing.T) {
	bad := "# coverage-engine v2\nmode: count\nb\t1.1,1.2\t1\t1\n"
	if _, err := Load(strings.NewReader(bad)); err == nil {
		t.Fatal("block before file must be rejected")
	}
	badMode := "# coverage-engine v2\nmode: bogus\n"
	if _, err := Load(strings.NewReader(badMode)); err == nil {
		t.Fatal("bad mode must be rejected")
	}
}

func TestFileRoundTrip(t *testing.T) {
	path := t.TempDir() + "/sub/cov.store"
	if err := SaveFile(path, sample()); err != nil {
		t.Fatal(err)
	}
	got, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.TotalStatements() != sample().TotalStatements() {
		t.Fatal("file roundtrip changed totals")
	}
}

func TestLoadMissingFileIsVersionError(t *testing.T) {
	_, err := LoadFile(t.TempDir() + "/absent")
	if _, ok := err.(*VersionError); !ok {
		t.Fatalf("want VersionError, got %T", err)
	}
}
