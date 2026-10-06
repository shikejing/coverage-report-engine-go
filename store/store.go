// Package store persists coverage in a versioned, backward compatible
// text format.
//
// File layout (v2, current):
//
//	# coverage-engine v2
//	mode: count
//	run: <runID>
//	missing <tab> <quoted shard> <tab> <kind> <tab> <quoted detail>
//	file <tab> <quoted importPath> <tab> <quoted relPath> <tab> <sha256>
//	b <tab> <sl>.<sc>,<el>.<ec> <tab> <numStmt> <tab> <count>
//
// v1 used the same magic with version "v1", grouped files under a single
// "file" field carrying importPath+"/"+relPath, and recorded neither
// source hashes nor missing ranges. v1 files remain fully readable; their
// hashes are simply empty. Files declaring an unknown or future-incompatible
// version are rejected at read time with a *VersionError.
package store

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/shikejing/coverage-report-engine-go/covermodel"
)

// Format versions understood by this build.
const (
	MinSupportedVersion = 1
	CurrentVersion      = 2
)

const magic = "# coverage-engine v"

// VersionError is returned when stored data cannot be read because of an
// incompatible version.
type VersionError struct {
	Got    int
	Min    int
	Max    int
	Reason string
}

func (e *VersionError) Error() string {
	return fmt.Sprintf("coverage store: incompatible format version %d (supported %d..%d): %s",
		e.Got, e.Min, e.Max, e.Reason)
}

// Save writes coverage using the current format version. Output is
// deterministic for identical input: packages, files, blocks, runs and
// missing ranges are sorted before serialization.
func Save(w io.Writer, c *covermodel.Coverage) error {
	if c == nil {
		return fmt.Errorf("coverage store: cannot save nil coverage")
	}
	if !c.Mode.Valid() {
		return fmt.Errorf("coverage store: cannot save unsupported mode %q", c.Mode)
	}
	c.Sort()
	bw := bufio.NewWriter(w)
	defer bw.Flush()

	fmt.Fprintf(bw, "%s%d\n", magic, CurrentVersion)
	fmt.Fprintf(bw, "mode: %s\n", c.Mode)
	for _, id := range c.RunIDs {
		fmt.Fprintf(bw, "run:\t%s\n", strconv.Quote(id))
	}
	for _, mr := range c.Missing {
		fmt.Fprintf(bw, "missing\t%s\t%s\t%s\n",
			strconv.Quote(mr.Shard), mr.Kind, strconv.Quote(mr.Detail))
	}
	for _, pkg := range c.Packages {
		for _, f := range pkg.Files {
			fmt.Fprintf(bw, "file\t%s\t%s\t%s\n",
				strconv.Quote(pkg.ImportPath), strconv.Quote(f.Path), f.Hash)
			for _, b := range f.Blocks {
				fmt.Fprintf(bw, "b\t%d.%d,%d.%d\t%d\t%d\n",
					b.StartLine, b.StartCol, b.EndLine, b.EndCol, b.NumStmt, b.Count)
			}
		}
	}
	return nil
}

// Load reads any supported format version, upgrading in memory to the
// current in-memory model.
func Load(r io.Reader) (*covermodel.Coverage, error) {
	br := bufio.NewReader(r)
	first, err := br.ReadString('\n')
	if err != nil && err != io.EOF {
		return nil, &VersionError{Min: MinSupportedVersion, Max: CurrentVersion, Reason: "unreadable header"}
	}
	first = strings.TrimRight(first, "\r\n")
	if !strings.HasPrefix(first, magic) {
		return nil, &VersionError{
			Min:    MinSupportedVersion,
			Max:    CurrentVersion,
			Reason: "missing \"" + magic + "N\" magic header; not a coverage-engine file",
		}
	}
	version, err := strconv.Atoi(strings.TrimPrefix(first, magic))
	if err != nil {
		return nil, &VersionError{Min: MinSupportedVersion, Max: CurrentVersion, Reason: "non-numeric version: " + strconv.Quote(first)}
	}
	if version < MinSupportedVersion || version > CurrentVersion {
		return nil, &VersionError{
			Got:    version,
			Min:    MinSupportedVersion,
			Max:    CurrentVersion,
			Reason: "version is outside the supported range",
		}
	}
	if version == 1 {
		return loadV1(br)
	}
	return loadV2(br)
}

func loadV2(r *bufio.Reader) (*covermodel.Coverage, error) {
	cov := &covermodel.Coverage{}
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	lineNo := 1
	var curPkg *covermodel.Package
	var curFile *covermodel.File

	failf := func(format string, args ...any) error {
		return fmt.Errorf("coverage store: corrupt v2 data at line %d: %s", lineNo, fmt.Sprintf(format, args...))
	}
	for scanner.Scan() {
		lineNo++
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		switch fields[0] {
		case "mode:":
			if len(fields) != 2 {
				return nil, failf("malformed mode line")
			}
			mode := covermodel.Mode(strings.TrimSpace(fields[1]))
			if !mode.Valid() {
				return nil, failf("unsupported mode %q", mode)
			}
			cov.Mode = mode
		case "run:":
			if len(fields) != 2 {
				return nil, failf("malformed run line")
			}
			id, err := strconv.Unquote(fields[1])
			if err != nil {
				return nil, failf("malformed run id: %v", err)
			}
			cov.RunIDs = append(cov.RunIDs, id)
		case "missing":
			if len(fields) != 4 {
				return nil, failf("malformed missing line")
			}
			shard, err := strconv.Unquote(fields[1])
			if err != nil {
				return nil, failf("malformed missing shard: %v", err)
			}
			detail, err := strconv.Unquote(fields[3])
			if err != nil {
				return nil, failf("malformed missing detail: %v", err)
			}
			cov.Missing = append(cov.Missing, covermodel.MissingRange{
				Shard: shard, Kind: fields[2], Detail: detail,
			})
		case "file":
			if len(fields) != 4 {
				return nil, failf("malformed file line")
			}
			importPath, err := strconv.Unquote(fields[1])
			if err != nil {
				return nil, failf("malformed import path: %v", err)
			}
			relPath, err := strconv.Unquote(fields[2])
			if err != nil {
				return nil, failf("malformed file path: %v", err)
			}
			curPkg = cov.Package(importPath)
			if curPkg == nil {
				curPkg = &covermodel.Package{ImportPath: importPath}
				cov.Packages = append(cov.Packages, curPkg)
			}
			curFile = &covermodel.File{Path: relPath, Hash: fields[3]}
			curPkg.Files = append(curPkg.Files, curFile)
		case "b":
			if curFile == nil {
				return nil, failf("block without preceding file")
			}
			if len(fields) != 4 {
				return nil, failf("malformed block line")
			}
			b, err := parseStoredSpan(fields[1], fields[2], fields[3])
			if err != nil {
				return nil, failf("%v", err)
			}
			curFile.Blocks = append(curFile.Blocks, b)
		default:
			return nil, failf("unknown record type %q", fields[0])
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("coverage store: read error: %w", err)
	}
	if !cov.Mode.Valid() {
		return nil, fmt.Errorf("coverage store: corrupt v2 data: no mode record")
	}
	cov.Sort()
	return cov, nil
}

func loadV1(r *bufio.Reader) (*covermodel.Coverage, error) {
	cov := &covermodel.Coverage{}
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	lineNo := 1
	var curPkg *covermodel.Package
	var curFile *covermodel.File
	failf := func(format string, args ...any) error {
		return fmt.Errorf("coverage store: corrupt v1 data at line %d: %s", lineNo, fmt.Sprintf(format, args...))
	}
	for scanner.Scan() {
		lineNo++
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		switch fields[0] {
		case "mode:":
			if len(fields) != 2 {
				return nil, failf("malformed mode line")
			}
			mode := covermodel.Mode(strings.TrimSpace(fields[1]))
			if !mode.Valid() {
				return nil, failf("unsupported mode %q", mode)
			}
			cov.Mode = mode
		case "run:":
			if len(fields) != 2 {
				return nil, failf("malformed run line")
			}
			cov.RunIDs = append(cov.RunIDs, strings.TrimSpace(fields[1]))
		case "file":
			// v1: file\t<quoted full import-qualified path>
			if len(fields) != 2 {
				return nil, failf("malformed v1 file line")
			}
			full, err := strconv.Unquote(fields[1])
			if err != nil {
				return nil, failf("malformed file path: %v", err)
			}
			importPath, relPath := splitFullPath(full)
			curPkg = cov.Package(importPath)
			if curPkg == nil {
				curPkg = &covermodel.Package{ImportPath: importPath}
				cov.Packages = append(cov.Packages, curPkg)
			}
			curFile = &covermodel.File{Path: relPath} // no hash in v1
			curPkg.Files = append(curPkg.Files, curFile)
		case "b":
			if curFile == nil {
				return nil, failf("block without preceding file")
			}
			if len(fields) != 4 {
				return nil, failf("malformed block line")
			}
			b, err := parseStoredSpan(fields[1], fields[2], fields[3])
			if err != nil {
				return nil, failf("%v", err)
			}
			curFile.Blocks = append(curFile.Blocks, b)
		default:
			return nil, failf("unknown record type %q (cannot be present in v1)", fields[0])
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("coverage store: read error: %w", err)
	}
	if !cov.Mode.Valid() {
		return nil, fmt.Errorf("coverage store: corrupt v1 data: no mode record")
	}
	cov.Sort()
	return cov, nil
}

// splitFullPath splits an import-qualified path "example.com/p/foo/x.go"
// into the package path and the file base name. v1 only stored one level
// of package directory nesting semantics identical to the coverprofile.
func splitFullPath(full string) (string, string) {
	full = covermodel.CanonicalPath(full)
	idx := strings.LastIndex(full, "/")
	if idx < 0 {
		return ".", full
	}
	return full[:idx], full[idx+1:]
}

func parseStoredSpan(span, numStmt, count string) (covermodel.Block, error) {
	comma := strings.Index(span, ",")
	if comma <= 0 {
		return covermodel.Block{}, fmt.Errorf("malformed span %q", span)
	}
	sl, sc, err := parsePoint(span[:comma])
	if err != nil {
		return covermodel.Block{}, err
	}
	el, ec, err := parsePoint(span[comma+1:])
	if err != nil {
		return covermodel.Block{}, err
	}
	n, err := strconv.Atoi(numStmt)
	if err != nil || n < 0 {
		return covermodel.Block{}, fmt.Errorf("invalid statement count %q", numStmt)
	}
	cnt, err := strconv.ParseInt(count, 10, 64)
	if err != nil || cnt < 0 {
		return covermodel.Block{}, fmt.Errorf("invalid hit count %q", count)
	}
	return covermodel.Block{StartLine: sl, StartCol: sc, EndLine: el, EndCol: ec, NumStmt: n, Count: cnt}, nil
}

func parsePoint(p string) (line, col int, err error) {
	dot := strings.Index(p, ".")
	if dot <= 0 {
		return 0, 0, fmt.Errorf("malformed point %q", p)
	}
	line, err = strconv.Atoi(p[:dot])
	if err != nil {
		return 0, 0, err
	}
	col, err = strconv.Atoi(p[dot+1:])
	if err != nil {
		return 0, 0, err
	}
	return line, col, nil
}

// PeekVersion returns the format version of serialized data without
// fully loading it.
func PeekVersion(data []byte) (int, error) {
	first, _, ok := bytes.Cut(data, []byte("\n"))
	if !ok {
		first = data
	}
	if !bytes.HasPrefix(first, []byte(magic)) {
		return 0, &VersionError{Min: MinSupportedVersion, Max: CurrentVersion, Reason: "missing magic header"}
	}
	v, err := strconv.Atoi(strings.TrimPrefix(string(first), magic))
	if err != nil {
		return 0, &VersionError{Min: MinSupportedVersion, Max: CurrentVersion, Reason: "non-numeric version"}
	}
	return v, nil
}
