package collect

import (
	"os"
	"testing"
)

func TestCollectSanity(t *testing.T) {
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	c := New("example.com/p", root+"/../testdata/src/example.com/p", "count")
	cov, err := c.CollectFile(root+"/../testdata/profiles/run1.out", "run-1")
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if got := cov.TotalStatements(); got != 5 {
		t.Fatalf("total statements = %d, want 5", got)
	}
	if got := cov.CoveredStatements(); got != 4 {
		t.Fatalf("covered statements = %d, want 4", got)
	}
	if len(cov.Packages) != 2 {
		t.Fatalf("packages = %d, want 2", len(cov.Packages))
	}
	if cov.Package("example.com/p/b").Files[0].Path != "b.go" {
		t.Fatalf("nested file name wrong: %+v", cov.Package("example.com/p/b").Files[0])
	}
}

func TestRejectCorrupt(t *testing.T) {
	c := New("example.com/p", "nope", "")
	if _, err := ParseProfile([]byte("not a profile\n"), "x"); err == nil {
		t.Fatal("expected corrupt error")
	} else if re, ok := AsRejectError(err); !ok || re.Kind != KindCorrupt {
		t.Fatalf("expected KindCorrupt, got %v", err)
	}
	// unknown source
	if _, err := c.Collect([]byte("mode: set\nfoo/bar/x.go:1.1,2.1 1 1\n"), "x", "r"); err == nil {
		t.Fatal("expected unknown source error")
	}
}
