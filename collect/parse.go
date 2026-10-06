package collect

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"

	"github.com/shikejing/coverage-report-engine-go/covermodel"
)

// RawRecord is one parsed coverprofile block before source verification.
type RawRecord struct {
	File  string
	Block covermodel.Block
}

// ParsedProfile is a syntactically parsed coverprofile.
type ParsedProfile struct {
	Mode    covermodel.Mode
	Records []RawRecord
}

// ParseProfile parses the coverprofile bytes emitted by
// `go test -coverprofile=...`.
//
// A profile must begin with a "mode:" declaration. Empty profiles (only
// the mode line, optionally with blank lines) are valid and represent a
// run with no covered/executable statements. Malformed input is rejected
// with a *RejectError of KindCorrupt so it is never mistaken for zero
// coverage.
func ParseProfile(data []byte, sourceName string) (*ParsedProfile, error) {
	reject := func(line int, reason string, args ...any) error {
		return &RejectError{
			Kind:   KindCorrupt,
			Source: sourceName,
			Line:   line,
			Reason: fmt.Sprintf(reason, args...),
		}
	}

	lines := bytes.Split(data, []byte("\n"))
	modeDeclared := false
	parsed := &ParsedProfile{}
	for i, raw := range lines {
		lineNo := i + 1
		line := strings.TrimSpace(string(raw))
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "mode:") {
			if lineNo != 1 {
				return nil, reject(lineNo, "mode declaration must appear on the first line")
			}
			if modeDeclared {
				return nil, reject(lineNo, "duplicate mode declaration")
			}
			mode := covermodel.Mode(strings.TrimSpace(strings.TrimPrefix(line, "mode:")))
			if !mode.Valid() {
				return nil, reject(lineNo, "unsupported mode %q", mode)
			}
			parsed.Mode = mode
			modeDeclared = true
			continue
		}
		if !modeDeclared {
			return nil, reject(lineNo, "missing leading \"mode:\" declaration")
		}
		rec, err := parseBlockLine(line)
		if err != nil {
			return nil, reject(lineNo, "%s", err.Error())
		}
		parsed.Records = append(parsed.Records, rec)
	}
	if !modeDeclared {
		return nil, reject(0, "missing leading \"mode:\" declaration")
	}
	return parsed, nil
}

// parseBlockLine parses one line of the form:
//
//	import/path/file.go:startLine.startCol,endLine.endCol numStmt count
func parseBlockLine(line string) (RawRecord, error) {
	fields := strings.Fields(line)
	if len(fields) != 3 {
		return RawRecord{}, fmt.Errorf("expected 3 fields (location, statements, count), got %d", len(fields))
	}
	loc := fields[0]
	colon := strings.LastIndex(loc, ":")
	if colon <= 0 {
		return RawRecord{}, fmt.Errorf("malformed block location %q", loc)
	}
	file := loc[:colon]
	span := loc[colon+1:]
	comma := strings.Index(span, ",")
	if comma <= 0 || comma == len(span)-1 {
		return RawRecord{}, fmt.Errorf("malformed block span %q", span)
	}
	start := span[:comma]
	end := span[comma+1:]
	sl, sc, err := parsePoint(start)
	if err != nil {
		return RawRecord{}, fmt.Errorf("malformed start position: %w", err)
	}
	el, ec, err := parsePoint(end)
	if err != nil {
		return RawRecord{}, fmt.Errorf("malformed end position: %w", err)
	}
	if sl > el || (sl == el && sc > ec) {
		return RawRecord{}, fmt.Errorf("block start is after end")
	}
	numStmt, err := strconv.Atoi(fields[1])
	if err != nil || numStmt < 0 {
		return RawRecord{}, fmt.Errorf("invalid statement count %q", fields[1])
	}
	count, err := strconv.ParseInt(fields[2], 10, 64)
	if err != nil || count < 0 {
		return RawRecord{}, fmt.Errorf("invalid hit count %q", fields[2])
	}
	return RawRecord{
		File: file,
		Block: covermodel.Block{
			StartLine: sl,
			StartCol:  sc,
			EndLine:   el,
			EndCol:    ec,
			NumStmt:   numStmt,
			Count:     count,
		},
	}, nil
}

func parsePoint(p string) (line, col int, err error) {
	dot := strings.Index(p, ".")
	if dot <= 0 || dot == len(p)-1 {
		return 0, 0, fmt.Errorf("expected line.column in %q", p)
	}
	line, err = strconv.Atoi(p[:dot])
	if err != nil {
		return 0, 0, fmt.Errorf("line %q: %w", p[:dot], err)
	}
	col, err = strconv.Atoi(p[dot+1:])
	if err != nil {
		return 0, 0, fmt.Errorf("column %q: %w", p[dot+1:], err)
	}
	if line <= 0 || col <= 0 {
		return 0, 0, fmt.Errorf("line and column must be positive in %q", p)
	}
	return line, col, nil
}
