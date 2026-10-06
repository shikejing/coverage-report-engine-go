package cover

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// Format selects the report encoding.
type Format string

const (
	FormatJSON Format = "json" // machine readable
	FormatText Format = "text" // human readable
)

// WriteReport streams a deterministic report of res to w. Identical
// inputs and config produce byte-identical output. The report drills down
// package -> file. Memory use is bounded by the largest single package,
// not by total data size: packages are written one at a time.
func WriteReport(w io.Writer, res *Result, f Format) error {
	switch f {
	case FormatJSON:
		return writeJSON(w, res)
	case FormatText:
		return writeText(w, res)
	default:
		return fmt.Errorf("cover: unknown report format %q", string(f))
	}
}

func writeJSON(w io.Writer, res *Result) error {
	// Result fields are already in canonical (sorted) order and encoding/json
	// emits struct fields in declaration order, so output is byte-stable.
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(res)
}

func writeText(w io.Writer, res *Result) error {
	var b strings.Builder
	fmt.Fprintf(&b, "coverage: %.2f%% of statements", res.Summary.Percent)
	if !res.Summary.Complete {
		b.WriteString(" (INCOMPLETE: some shards missing)")
	}
	b.WriteString("\n")
	if _, err := io.WriteString(w, b.String()); err != nil {
		return err
	}
	for _, ps := range res.Packages {
		var pb strings.Builder
		fmt.Fprintf(&pb, "%s\t%.2f%%\n", ps.Path, ps.Percent)
		for _, fs := range ps.Files {
			fmt.Fprintf(&pb, "  %s\t%.2f%%\t%d/%d statements\n", fs.Path, fs.Percent, fs.Covered, fs.Statements)
		}
		if _, err := io.WriteString(w, pb.String()); err != nil {
			return err
		}
	}
	if len(res.Missing) > 0 {
		var mb strings.Builder
		mb.WriteString("missing ranges:\n")
		for _, mr := range res.Missing {
			fmt.Fprintf(&mb, "  %s:%d-%d\t%s\n", mr.Path, mr.StartLine, mr.EndLine, mr.Reason)
		}
		if _, err := io.WriteString(w, mb.String()); err != nil {
			return err
		}
	}
	return nil
}
