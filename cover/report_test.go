package cover

import (
	"bytes"
	"strings"
	"testing"
)

func sampleResult(t *testing.T) *Result {
	t.Helper()
	return resultFrom(t, map[string]string{
		"r1": `mode: count
pkg/a.go:1.1,2.1 2 1
pkg/a.go:3.1,4.1 1 0
pkg/sub/b.go:1.1,2.1 3 2
`,
	})
}

func TestReportByteIdentical(t *testing.T) {
	res := sampleResult(t)
	for _, f := range []Format{FormatJSON, FormatText} {
		var b1, b2 bytes.Buffer
		if err := WriteReport(&b1, res, f); err != nil {
			t.Fatal(err)
		}
		// Rebuild the same result from scratch to catch map-order leaks.
		res2 := sampleResult(t)
		if err := WriteReport(&b2, res2, f); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(b1.Bytes(), b2.Bytes()) {
			t.Fatalf("format %s not byte-identical:\n%s\n---\n%s", f, b1.Bytes(), b2.Bytes())
		}
	}
}

func TestReportDrillDown(t *testing.T) {
	res := sampleResult(t)
	var buf bytes.Buffer
	if err := WriteReport(&buf, res, FormatText); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"coverage:", "pkg\t", "pkg/sub\t", "pkg/a.go", "pkg/sub/b.go"} {
		if !strings.Contains(out, want) {
			t.Fatalf("text report missing %q:\n%s", want, out)
		}
	}
}

func TestReportMissingRanges(t *testing.T) {
	res := sampleResult(t)
	res.Summary.Complete = false
	res.Missing = []MissingRange{{Path: "pkg/c.go", StartLine: 1, EndLine: -1, Reason: "shard_timeout"}}
	var buf bytes.Buffer
	if err := WriteReport(&buf, res, FormatText); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "INCOMPLETE") || !strings.Contains(buf.String(), "pkg/c.go") {
		t.Fatalf("missing ranges not reported:\n%s", buf.String())
	}
	var jb bytes.Buffer
	if err := WriteReport(&jb, res, FormatJSON); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(jb.String(), `"complete": false`) {
		t.Fatalf("json missing complete flag:\n%s", jb.String())
	}
}

func TestReportUnknownFormat(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteReport(&buf, sampleResult(t), Format("xml")); err == nil {
		t.Fatal("want error for unknown format")
	}
}
