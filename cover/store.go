package cover

import (
	"encoding/binary"
	"encoding/json"
	"io"
)

// Storage format. Layout: magic(4) | version(2) | payload.
// v1 payload: JSON of diskV1 (files only).
// v2 payload: JSON of diskV2 (files + run IDs + content hashes).
// Readers accept every version in [MinVersion, Version]; anything else is
// rejected at read time with ReasonIncompatVersion, so incompatible data
// never reaches the merge stage.
const (
	Magic      uint32 = 0x434f5652 // "COVR"
	MinVersion uint16 = 1
	Version    uint16 = 2
)

type diskFile struct {
	Path   string           `json:"path"`
	Blocks map[string]int64 `json:"blocks"`
	Meta   map[string]Block `json:"meta"`
}

type diskV1 struct {
	Mode  string     `json:"mode"`
	Files []diskFile `json:"files"`
}

type diskV2 struct {
	Mode   string            `json:"mode"`
	Runs   []string          `json:"runs"`
	Hashes map[string]string `json:"hashes,omitempty"`
	Files  []diskFile        `json:"files"`
}

// Save serializes the merger state in the current format version.
func Save(w io.Writer, m *Merger) error {
	var hdr [6]byte
	binary.BigEndian.PutUint32(hdr[0:4], Magic)
	binary.BigEndian.PutUint16(hdr[4:6], Version)
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	d := diskV2{
		Mode:   string(m.mode),
		Runs:   m.Runs(),
		Hashes: m.Hashes(),
	}
	for _, f := range m.Files() {
		d.Files = append(d.Files, diskFile{Path: f.Path, Blocks: f.Blocks, Meta: f.Meta})
	}
	enc, err := json.Marshal(d)
	if err != nil {
		return err
	}
	_, err = w.Write(enc)
	return err
}

// Load deserializes merger state written by Save, accepting all
// backward-compatible versions and rejecting incompatible ones with a
// distinguishable reason.
func Load(r io.Reader) (*Merger, error) {
	var hdr [6]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, reject(ReasonCorruptPayload, "short header: %v", err)
	}
	if binary.BigEndian.Uint32(hdr[0:4]) != Magic {
		return nil, reject(ReasonBadMagic, "not a cover store (bad magic)")
	}
	ver := binary.BigEndian.Uint16(hdr[4:6])
	if ver < MinVersion || ver > Version {
		return nil, reject(ReasonIncompatVersion, "store version %d not in supported range [%d,%d]", ver, MinVersion, Version)
	}
	payload, err := io.ReadAll(r)
	if err != nil {
		return nil, reject(ReasonCorruptPayload, "read payload: %v", err)
	}
	m := NewMerger()
	switch ver {
	case 1:
		var d diskV1
		if err := json.Unmarshal(payload, &d); err != nil {
			return nil, reject(ReasonCorruptPayload, "v1 payload: %v", err)
		}
		m.mode = Mode(d.Mode)
		m.hasRun = true
		for _, f := range d.Files {
			m.files[f.Path] = &FileCoverage{Path: f.Path, Blocks: f.Blocks, Meta: f.Meta}
		}
	case 2:
		var d diskV2
		if err := json.Unmarshal(payload, &d); err != nil {
			return nil, reject(ReasonCorruptPayload, "v2 payload: %v", err)
		}
		m.mode = Mode(d.Mode)
		m.hasRun = true
		for _, id := range d.Runs {
			m.runs[id] = struct{}{}
		}
		for k, v := range d.Hashes {
			m.hashes[k] = v
		}
		for _, f := range d.Files {
			m.files[f.Path] = &FileCoverage{Path: f.Path, Blocks: f.Blocks, Meta: f.Meta}
		}
	}
	return m, nil
}
