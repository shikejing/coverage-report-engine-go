package cover

import (
	"bufio"
	"fmt"
	"io"
	"path"
	"strconv"
	"strings"
)

// Mode is the coverage counting mode declared by a profile header.
type Mode string

const (
	ModeSet    Mode = "set"
	ModeCount  Mode = "count"
	ModeAtomic Mode = "atomic"
)

// Profile is a parsed coverage profile: one run's raw result.
type Profile struct {
	Mode  Mode
	RunID string // logical identity of the producing run; used for dedup
	Files []*FileCoverage
}

// ParseProfile reads a Go coverprofile text stream and validates it.
// root is the module root used to verify provenance: any file path that
// escapes root is rejected with ReasonUnknownSource. Malformed or
// headerless input is rejected with a distinguishable reason; it is never
// silently treated as zero hits.
func ParseProfile(r io.Reader, root string, runID string) (*Profile, error) {
	root = path.Clean("/" + root)
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<20)

	lineNo := 0
	if !sc.Scan() {
		if err := sc.Err(); err != nil {
			return nil, reject(ReasonCorruptPayload, "read error: %v", err)
		}
		return nil, reject(ReasonMissingMode, "empty profile: first line must be 'mode: <set|count|atomic>'")
	}
	lineNo++
	header := strings.TrimSpace(sc.Text())
	if !strings.HasPrefix(header, "mode:") {
		return nil, reject(ReasonMissingMode, "line 1: got %q, want 'mode: <set|count|atomic>'", header)
	}
	mode := Mode(strings.TrimSpace(strings.TrimPrefix(header, "mode:")))
	switch mode {
	case ModeSet, ModeCount, ModeAtomic:
	default:
		return nil, reject(ReasonUnknownMode, "line 1: unsupported mode %q", string(mode))
	}

	p := &Profile{Mode: mode, RunID: runID}
	byPath := map[string]*FileCoverage{}
	var order []string

	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		name, blk, count, err := parseProfileLine(line)
		if err != nil {
			return nil, reject(ReasonMalformedLine, "line %d: %v", lineNo, err)
		}
		// Provenance check: normalize and require containment under root.
		clean := path.Clean("/" + name)
		if root != "/" && !strings.HasPrefix(clean, root+"/") {
			return nil, reject(ReasonUnknownSource, "line %d: file %q is outside root %q", lineNo, name, root)
		}
		rel := strings.TrimPrefix(clean, root+"/")
		if root == "/" {
			rel = strings.TrimPrefix(clean, "/")
		}

		fc := byPath[rel]
		if fc == nil {
			fc = &FileCoverage{Path: rel, Blocks: map[string]int64{}, Meta: map[string]Block{}}
			byPath[rel] = fc
			order = append(order, rel)
		}
		k := blk.key()
		if prev, dup := fc.Meta[k]; dup && prev != blk {
			return nil, reject(ReasonDupBlockGeom, "line %d: block %s conflicts with earlier geometry", lineNo, k)
		}
		fc.Meta[k] = blk
		fc.Blocks[k] += count
	}
	if err := sc.Err(); err != nil {
		return nil, reject(ReasonCorruptPayload, "read error: %v", err)
	}
	for _, name := range order {
		p.Files = append(p.Files, byPath[name])
	}
	return p, nil
}

// parseProfileLine parses "file:startLine.startCol,endLine.endCol numStmt count".
func parseProfileLine(line string) (name string, blk Block, count int64, err error) {
	colon := strings.LastIndex(line, ":")
	if colon < 0 {
		return "", blk, 0, errLine("missing ':' separator")
	}
	name = line[:colon]
	rest := line[colon+1:]
	fields := strings.Fields(rest)
	if len(fields) != 3 {
		return "", blk, 0, errLine("want 3 fields after file name, got %d", len(fields))
	}
	if name == "" {
		return "", blk, 0, errLine("empty file name")
	}
	pos := fields[0]
	comma := strings.Index(pos, ",")
	if comma < 0 {
		return "", blk, 0, errLine("missing ',' in position range")
	}
	if blk.StartLine, blk.StartCol, err = parsePos(pos[:comma]); err != nil {
		return "", blk, 0, err
	}
	if blk.EndLine, blk.EndCol, err = parsePos(pos[comma+1:]); err != nil {
		return "", blk, 0, err
	}
	if blk.StartLine <= 0 || blk.EndLine < blk.StartLine {
		return "", blk, 0, errLine("invalid line range %d-%d", blk.StartLine, blk.EndLine)
	}
	ns, err := strconv.Atoi(fields[1])
	if err != nil || ns < 0 {
		return "", blk, 0, errLine("bad statement count %q", fields[1])
	}
	blk.NumStmt = ns
	c, err := strconv.ParseInt(fields[2], 10, 64)
	if err != nil || c < 0 {
		return "", blk, 0, errLine("bad hit count %q", fields[2])
	}
	return name, blk, c, nil
}

func parsePos(s string) (line, col int, err error) {
	dot := strings.Index(s, ".")
	if dot < 0 {
		return 0, 0, errLine("bad position %q", s)
	}
	line, err1 := strconv.Atoi(s[:dot])
	col, err2 := strconv.Atoi(s[dot+1:])
	if err1 != nil || err2 != nil || line < 0 || col < 0 {
		return 0, 0, errLine("bad position %q", s)
	}
	return line, col, nil
}

type lineError string

func errLine(format string, args ...any) error {
	return lineError(fmt.Sprintf(format, args...))
}

func (e lineError) Error() string { return string(e) }
