package cover

import (
	"errors"
	"strings"
	"testing"
)

func mustReject(t *testing.T, err error, want RejectReason) {
	t.Helper()
	var rej *RejectError
	if !errors.As(err, &rej) {
		t.Fatalf("want RejectError, got %v", err)
	}
	if rej.Reason != want {
		t.Fatalf("want reason %q, got %q (%v)", want, rej.Reason, err)
	}
}

func TestParseValidMultiPackage(t *testing.T) {
	in := `mode: count
pkg/a/a.go:3.14,5.2 2 3
pkg/a/a.go:7.2,9.3 1 0
pkg/nested/deep/b.go:2.1,4.1 5 7
`
	p, err := ParseProfile(strings.NewReader(in), ".", "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if p.Mode != ModeCount {
		t.Fatalf("mode = %q", p.Mode)
	}
	if len(p.Files) != 2 {
		t.Fatalf("files = %d", len(p.Files))
	}
	a := p.Files[0]
	if a.Path != "pkg/a/a.go" {
		t.Fatalf("path = %q", a.Path)
	}
	var total int64
	for _, c := range a.Blocks {
		total += c
	}
	if total != 3 {
		t.Fatalf("total hits = %d", total)
	}
}

func TestParseRejects(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want RejectReason
	}{
		{"empty", "", ReasonMissingMode},
		{"no header", "pkg/a.go:1.1,2.1 1 1\n", ReasonMissingMode},
		{"bad mode", "mode: fuzzy\n", ReasonUnknownMode},
		{"malformed", "mode: set\nnot a line\n", ReasonMalformedLine},
		{"bad count", "mode: set\npkg/a.go:1.1,2.1 1 x\n", ReasonMalformedLine},
		{"negative hits", "mode: set\npkg/a.go:1.1,2.1 1 -2\n", ReasonMalformedLine},
		{"escape root", "mode: set\n../outside/x.go:1.1,2.1 1 1\n", ReasonUnknownSource},
		{"absolute outside", "mode: set\n/etc/passwd:1.1,2.1 1 1\n", ReasonUnknownSource},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseProfile(strings.NewReader(tc.in), "mod", "r")
			mustReject(t, err, tc.want)
		})
	}
}

func TestParseDuplicateBlockAccumulatesWithinProfile(t *testing.T) {
	in := "mode: count\npkg/a.go:1.1,2.1 1 2\npkg/a.go:1.1,2.1 1 3\n"
	p, err := ParseProfile(strings.NewReader(in), ".", "r")
	if err != nil {
		t.Fatal(err)
	}
	if got := p.Files[0].Blocks["1:1:2:1:1"]; got != 5 {
		t.Fatalf("hits = %d", got)
	}
}
