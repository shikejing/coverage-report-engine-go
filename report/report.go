// Package report renders coverage as machine-readable (JSON) and
// human-readable (text) output. Output is streamed and deterministic:
// the same coverage and options always produce byte-identical results.
package report

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/shikejing/coverage-report-engine-go/covermodel"
)

// Format selects a report renderer.
type Format string

// Supported formats.
const (
	FormatJSON Format = "json"
	FormatText Format = "text"
)

// Options configures a report.
type Options struct {
	Format Format
	// Package filters the report to one import path; empty means all.
	Package string
	// File filters to one package-relative file; requires Package.
	File string
	// BlockDetail emits per-block hit counts in text reports.
	BlockDetail bool
}

// JSON model types, ordered for stable output.

type jsonBlock struct {
	StartLine int   `json:"startLine"`
	StartCol  int   `json:"startCol"`
	EndLine   int   `json:"endLine"`
	EndCol    int   `json:"endCol"`
	NumStmt   int   `json:"numStmt"`
	Hits      int64 `json:"hits"`
}

type jsonFile struct {
	Path       string      `json:"path"`
	Hash       string      `json:"hash,omitempty"`
	TotalStmt  int         `json:"totalStmt"`
	Covered    int         `json:"coveredStmt"`
	Percent    float64     `json:"percent"`
	Blocks     []jsonBlock `json:"blocks,omitempty"`
}

type jsonPackage struct {
	ImportPath string     `json:"importPath"`
	TotalStmt  int        `json:"totalStmt"`
	Covered    int        `json:"coveredStmt"`
	Percent    float64    `json:"percent"`
	Files      []jsonFile `json:"files"`
}

type jsonMissing struct {
	Shard  string `json:"shard"`
	Kind   string `json:"kind"`
	Detail string `json:"detail"`
}

type jsonReport struct {
	Schema       string        `json:"schema"`
	Mode         string        `json:"mode"`
	TotalStmt    int           `json:"totalStmt"`
	CoveredStmt  int           `json:"coveredStmt"`
	Percent      float64       `json:"percent"`
	Runs         []string      `json:"runs"`
	Missing      []jsonMissing `json:"missing"`
	Packages     []jsonPackage `json:"packages"`
}

// Write streams a report to w. The renderer iterates packages, files and
// blocks directly from the model; it never builds a second full copy of
// the data, so extra memory is bounded by the buffered writer and one
// JSON record at a time.
func Write(w io.Writer, cov *covermodel.Coverage, opts Options) error {
	if cov == nil {
		return fmt.Errorf("report: nil coverage")
	}
	bw := bufio.NewWriter(w)
	defer bw.Flush()
	switch opts.Format {
	case FormatJSON:
		return writeJSON(bw, cov, opts)
	case FormatText:
		return writeText(bw, cov, opts)
	default:
		return fmt.Errorf("report: unsupported format %q", opts.Format)
	}
}

func fileStats(f *covermodel.File) (total, covered int) {
	for _, b := range f.Blocks {
		total += b.NumStmt
		if b.Count > 0 {
			covered += b.NumStmt
		}
	}
	return total, covered
}

func pct(total, covered int) float64 {
	if total == 0 {
		return 100
	}
	return float64(covered) * 100 / float64(total)
}

func writeJSON(w *bufio.Writer, cov *covermodel.Coverage, opts Options) error {
	// A streaming encoder is used so very large reports do not require
	// building their complete serialized form in memory.
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")

	r := jsonReport{
		Schema:      "coverage-report/v1",
		Mode:        string(cov.Mode),
		TotalStmt:   cov.TotalStatements(),
		CoveredStmt: cov.CoveredStatements(),
		Percent:     round2(cov.Percent()),
		Runs:        cov.RunIDs,
	}
	if len(cov.Missing) > 0 {
		for _, m := range cov.Missing {
			r.Missing = append(r.Missing, jsonMissing{Shard: m.Shard, Kind: m.Kind, Detail: m.Detail})
		}
	}
	for _, pkg := range cov.Packages {
		if opts.Package != "" && pkg.ImportPath != opts.Package {
			continue
		}
		jp := jsonPackage{ImportPath: pkg.ImportPath}
		for _, f := range pkg.Files {
			if opts.File != "" && f.Path != opts.File {
				continue
			}
			t, c := fileStats(f)
			jf := jsonFile{
				Path:      f.Path,
				Hash:      f.Hash,
				TotalStmt: t,
				Covered:   c,
				Percent:   round2(pct(t, c)),
			}
			if opts.BlockDetail {
				for _, b := range f.Blocks {
					jf.Blocks = append(jf.Blocks, jsonBlock{
						StartLine: b.StartLine, StartCol: b.StartCol,
						EndLine: b.EndLine, EndCol: b.EndCol,
						NumStmt: b.NumStmt, Hits: b.Count,
					})
				}
			}
			jp.Files = append(jp.Files, jf)
			jp.TotalStmt += t
			jp.Covered += c
		}
		jp.Percent = round2(pct(jp.TotalStmt, jp.Covered))
		r.Packages = append(r.Packages, jp)
	}
	if r.Runs == nil {
		r.Runs = []string{}
	}
	if r.Missing == nil {
		r.Missing = []jsonMissing{}
	}
	if r.Packages == nil {
		r.Packages = []jsonPackage{}
	}
	return enc.Encode(&r)
}

func writeText(w *bufio.Writer, cov *covermodel.Coverage, opts Options) error {
	printf := func(format string, args ...any) {
		fmt.Fprintf(w, format, args...)
	}
	printf("coverage report (mode: %s)\n", cov.Mode)
	printf("total: %d/%d statements  %.2f%%\n",
		cov.CoveredStatements(), cov.TotalStatements(), cov.Percent())
	if len(cov.RunIDs) > 0 {
		printf("runs: %s\n", strings.Join(cov.RunIDs, ", "))
	}
	if len(cov.Missing) > 0 {
		printf("missing ranges (%d):\n", len(cov.Missing))
		for _, m := range cov.Missing {
			printf("  ! [%s] %s: %s\n", m.Kind, m.Shard, m.Detail)
		}
	}
	for _, pkg := range cov.Packages {
		if opts.Package != "" && pkg.ImportPath != opts.Package {
			continue
		}
		pt, pc := 0, 0
		var rows [][3]int // total, covered, line count of path
		var files []*covermodel.File
		for _, f := range pkg.Files {
			if opts.File != "" && f.Path != opts.File {
				continue
			}
			t, c := fileStats(f)
			pt, pc = pt+t, pc+c
			rows = append(rows, [3]int{t, c, len(f.Path)})
			files = append(files, f)
		}
		printf("\npackage %s  %d/%d  %.2f%%\n", pkg.ImportPath, pc, pt, pct(pt, pc))
		maxPath := 4
		for _, r := range rows {
			if r[2] > maxPath {
				maxPath = r[2]
			}
		}
		printf("  %-*s  stmt  covered  percent\n", maxPath, "file")
		for i, f := range files {
			t, c := rows[i][0], rows[i][1]
			mark := " "
			if t > 0 && c == 0 {
				mark = "!"
			}
			printf("  %s%-*s  %4d  %7d  %6.2f%%\n", mark, maxPath, f.Path, t, c, pct(t, c))
			if opts.BlockDetail {
				for _, b := range f.Blocks {
					hit := " "
					if b.Count == 0 {
						hit = "x"
					}
					printf("      %s %d.%d-%d.%d stmt=%d hits=%d\n",
						hit, b.StartLine, b.StartCol, b.EndLine, b.EndCol, b.NumStmt, b.Count)
				}
			}
		}
	}
	return nil
}

func round2(v float64) float64 {
	return float64(int64(v*100+0.5)) / 100
}
