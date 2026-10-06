package cover

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"strings"
	"testing"
)

func TestStoreRoundTrip(t *testing.T) {
	m := NewMerger()
	if _, err := m.Import(prof(t, "run-1", runA)); err != nil {
		t.Fatal(err)
	}
	m.SetContentHash("pkg/a.go", "hash-a")
	var buf bytes.Buffer
	if err := Save(&buf, m); err != nil {
		t.Fatal(err)
	}
	m2, err := Load(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if got := m2.File("pkg/a.go").Blocks["1:1:2:1:1"]; got != 1 {
		t.Fatalf("hits = %d", got)
	}
	if m2.ContentHash("pkg/a.go") != "hash-a" {
		t.Fatal("hash lost")
	}
	// Run IDs survive, so re-importing the same run after reload is a no-op.
	ok, err := m2.Import(prof(t, "run-1", runA))
	if err != nil || ok {
		t.Fatalf("reimport after reload: ok=%v err=%v", ok, err)
	}
}

func TestStoreV1BackwardCompatible(t *testing.T) {
	// Hand-build a v1 store: files only, no runs/hashes.
	v1 := diskV1{
		Mode: "count",
		Files: []diskFile{{
			Path:   "pkg/a.go",
			Blocks: map[string]int64{"1:1:2:1:1": 7},
			Meta:   map[string]Block{"1:1:2:1:1": {StartLine: 1, StartCol: 1, EndLine: 2, EndCol: 1, NumStmt: 1}},
		}},
	}
	payload, _ := json.Marshal(v1)
	var buf bytes.Buffer
	var hdr [6]byte
	binary.BigEndian.PutUint32(hdr[0:4], Magic)
	binary.BigEndian.PutUint16(hdr[4:6], 1)
	buf.Write(hdr[:])
	buf.Write(payload)

	m, err := Load(&buf)
	if err != nil {
		t.Fatalf("v1 must remain readable: %v", err)
	}
	if got := m.File("pkg/a.go").Blocks["1:1:2:1:1"]; got != 7 {
		t.Fatalf("hits = %d", got)
	}
	// And it must merge with current-version data.
	if _, err := m.Import(prof(t, "new", runA)); err != nil {
		t.Fatal(err)
	}
	if got := m.File("pkg/a.go").Blocks["1:1:2:1:1"]; got != 8 {
		t.Fatalf("merged hits = %d", got)
	}
}

func TestStoreRejectsIncompatible(t *testing.T) {
	var buf bytes.Buffer
	var hdr [6]byte
	binary.BigEndian.PutUint32(hdr[0:4], Magic)
	binary.BigEndian.PutUint16(hdr[4:6], 99)
	buf.Write(hdr[:])
	buf.WriteString("{}")
	_, err := Load(&buf)
	mustReject(t, err, ReasonIncompatVersion)
}

func TestStoreRejectsBadMagicAndCorrupt(t *testing.T) {
	_, err := Load(strings.NewReader("XXXXXX{}"))
	mustReject(t, err, ReasonBadMagic)

	var buf bytes.Buffer
	var hdr [6]byte
	binary.BigEndian.PutUint32(hdr[0:4], Magic)
	binary.BigEndian.PutUint16(hdr[4:6], 2)
	buf.Write(hdr[:])
	buf.WriteString("{not json")
	_, err = Load(&buf)
	mustReject(t, err, ReasonCorruptPayload)
}
