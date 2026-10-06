package cover

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEngineIngestDedupAndReject(t *testing.T) {
	e := NewEngine(Config{Root: "."})
	m := NewMerger()
	ok, err := e.Ingest(m, strings.NewReader("mode: count\npkg/a.go:1.1,2.1 1 1\n"), "run-1")
	if !ok || err != nil {
		t.Fatalf("first ingest: ok=%v err=%v", ok, err)
	}
	// Same run ID again: no-op, no double count.
	ok, err = e.Ingest(m, strings.NewReader("mode: count\npkg/a.go:1.1,2.1 1 1\n"), "run-1")
	if ok || err != nil {
		t.Fatalf("dup ingest: ok=%v err=%v", ok, err)
	}
	if got := m.File("pkg/a.go").Blocks["1:1:2:1:1"]; got != 1 {
		t.Fatalf("hits = %d", got)
	}
	// Corrupt input is rejected, not zero-hit.
	_, err = e.Ingest(m, strings.NewReader("garbage"), "run-2")
	mustReject(t, err, ReasonMissingMode)
}

func TestEngineCollectShardsEndToEnd(t *testing.T) {
	root := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("pkg/a.go", "package a\n")
	write("pkg/sub/b.go", "package b\n")
	write("vendor/x/x.go", "package x\n") // skipped

	files, err := ListGoFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("files = %v", files)
	}
	if _, err := HashFile(filepath.Join(root, "pkg/a.go")); err != nil {
		t.Fatal(err)
	}

	e := NewEngine(Config{Root: ".", ShardCount: 2, MaxParallel: 2, ShardTimeout: time.Second})
	plan := PlanShards(files, 2)
	streams := map[int]io.Reader{}
	for i, shard := range plan.Shards {
		var b strings.Builder
		b.WriteString("mode: count\n")
		for _, f := range shard {
			b.WriteString(f + ":1.1,2.1 1 1\n")
		}
		streams[i] = strings.NewReader(b.String())
	}
	m, missing, err := e.CollectShards(context.Background(), streams, plan)
	if err != nil {
		t.Fatal(err)
	}
	res := m.Result(missing)
	if !res.Summary.Complete || res.Summary.Covered != 2 || res.Summary.Statements != 2 {
		t.Fatalf("summary = %+v missing=%v", res.Summary, missing)
	}
}

func TestEngineCollectShardsMissingStream(t *testing.T) {
	e := NewEngine(Config{Root: ".", ShardCount: 2})
	plan := PlanShards([]string{"a.go", "b.go"}, 2)
	// Provide no streams at all: every shard fails, everything is missing,
	// and coverage is marked incomplete rather than silently 0%.
	m, missing, err := e.CollectShards(context.Background(), map[int]io.Reader{}, plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) != 2 {
		t.Fatalf("missing = %v", missing)
	}
	res := m.Result(missing)
	if res.Summary.Complete {
		t.Fatal("must be incomplete")
	}
	if res.Summary.Statements != 0 {
		t.Fatalf("no statements should be counted, got %+v", res.Summary)
	}
}
