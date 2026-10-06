package collect

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/shikejing/coverage-report-engine-go/covermodel"
)

func fixtureRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(wd, "..", "testdata", "src", "example.com", "p")
	if _, err := os.Stat(filepath.Join(root, "a.go")); err != nil {
		t.Fatalf("fixture missing: %v", err)
	}
	return root
}

func TestCollectNestedPackages(t *testing.T) {
	c := New("example.com/p", fixtureRoot(t), "count")
	data := []byte("mode: count\n" +
		"example.com/p/a.go:3.31,5.2 1 1\n" +
		"example.com/p/a.go:5.2,6.15 2 1\n" +
		"example.com/p/a.go:6.2,8.1 1 0\n" +
		"example.com/p/b/b.go:3.30,5.2 1 1\n")
	cov, err := c.Collect(data, "run.out", "r1")
	if err != nil {
		t.Fatal(err)
	}
	if cov.TotalStatements() != 5 || cov.CoveredStatements() != 4 {
		t.Fatalf("stats = %d/%d, want 4/5", cov.CoveredStatements(), cov.TotalStatements())
	}
	if len(cov.Packages) != 2 {
		t.Fatalf("packages = %d want 2", len(cov.Packages))
	}
	if cov.File("example.com/p", "a.go") == nil {
		t.Fatal("root package file a.go missing")
	}
	bf := cov.File("example.com/p/b", "b.go")
	if bf == nil || bf.Hash == "" {
		t.Fatal("nested package file missing or unhashed")
	}
	if cov.RunIDs[0] != "r1" {
		t.Fatalf("run id = %v want r1", cov.RunIDs)
	}
}

func TestRejectMissingFile(t *testing.T) {
	c := New("example.com/p", fixtureRoot(t), "count")
	_, err := c.CollectFile(filepath.Join(t.TempDir(), "nope.out"), "r")
	re, ok := AsRejectError(err)
	if !ok || re.Kind != KindMissing {
		t.Fatalf("want KindMissing, got %v", err)
	}
}

func TestRejectCorruptVariants(t *testing.T) {
	cases := map[string]string{
		"no mode":        "example.com/p/a.go:3.31,5.2 1 1\n",
		"bad mode":       "mode: weird\nexample.com/p/a.go:3.31,5.2 1 1\n",
		"bad fields":     "mode: set\nnonsense-line\n",
		"bad location":   "mode: set\nexample.com/p/a.go:garbage 1 1\n",
		"neg count":      "mode: set\nexample.com/p/a.go:3.31,5.2 1 -1\n",
		"start after end": "mode: set\nexample.com/p/a.go:9.1,2.1 1 1\n",
	}
	for name, in := range cases {
		if _, err := ParseProfile([]byte(in), "x"); err == nil {
			t.Fatalf("%s: expected corrupt error", name)
		} else if re, ok := AsRejectError(err); !ok || re.Kind != KindCorrupt {
			t.Fatalf("%s: want KindCorrupt got %v", name, err)
		}
	}
}

func TestRejectUnknownSource(t *testing.T) {
	c := New("example.com/p", fixtureRoot(t), "")
	data := []byte("mode: set\nother.com/mod/x.go:1.1,2.1 1 1\n")
	_, err := c.Collect(data, "x", "r")
	re, ok := AsRejectError(err)
	if !ok || re.Kind != KindUnknownSource {
		t.Fatalf("want KindUnknownSource, got %v", err)
	}
	// references a path inside module prefix that does not exist on disk
	data2 := []byte("mode: set\nexample.com/p/ghost/x.go:1.1,2.1 1 1\n")
	_, err = c.Collect(data2, "x", "r")
	if re, ok = AsRejectError(err); !ok || re.Kind != KindUnknownSource {
		t.Fatalf("missing on-disk source: want KindUnknownSource, got %v", err)
	}
}

func TestRejectModeMismatch(t *testing.T) {
	c := New("example.com/p", fixtureRoot(t), "atomic")
	data := []byte("mode: set\nexample.com/p/a.go:3.31,5.2 1 1\n")
	_, err := c.Collect(data, "x", "r")
	if re, ok := AsRejectError(err); !ok || re.Kind != KindModeMismatch {
		t.Fatalf("want KindModeMismatch, got %v", err)
	}
}

func TestRejectOutOfRangeBlock(t *testing.T) {
	c := New("example.com/p", fixtureRoot(t), "set")
	data := []byte("mode: set\nexample.com/p/a.go:100.1,101.1 1 0\n")
	_, err := c.Collect(data, "x", "r")
	if re, ok := AsRejectError(err); !ok || re.Kind != KindCorrupt {
		t.Fatalf("want KindCorrupt for block beyond EOF, got %v", err)
	}
}

func TestEmptyProfileIsValid(t *testing.T) {
	c := New("example.com/p", fixtureRoot(t), "")
	cov, err := c.Collect([]byte("mode: set\n\n"), "x", "r")
	if err != nil {
		t.Fatal(err)
	}
	if cov.Mode != covermodel.ModeSet || len(cov.Packages) != 0 {
		t.Fatalf("empty profile mishandled: %+v", cov)
	}
}

func TestSetModeAggregation(t *testing.T) {
	c := New("example.com/p", fixtureRoot(t), "set")
	// same range listed twice, one hit one miss -> set mode yields hit
	data := []byte("mode: set\n" +
		"example.com/p/a.go:3.31,5.2 1 0\n" +
		"example.com/p/a.go:3.31,5.2 1 1\n")
	cov, err := c.Collect(data, "x", "r")
	if err != nil {
		t.Fatal(err)
	}
	b := cov.File("example.com/p", "a.go").Blocks[0]
	if b.Count != 1 {
		t.Fatalf("set aggregation = %d want 1", b.Count)
	}
}
