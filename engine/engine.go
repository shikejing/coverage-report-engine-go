// Package engine is the stable facade over collection, merging, storage,
// incremental collection, gating and reporting. Entry points in this
// package are kept stable across storage format evolutions so existing
// callers do not have to migrate when the on-disk format gains versions.
package engine

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"time"

	"github.com/shikejing/coverage-report-engine-go/collect"
	"github.com/shikejing/coverage-report-engine-go/config"
	"github.com/shikejing/coverage-report-engine-go/covermodel"
	"github.com/shikejing/coverage-report-engine-go/incremental"
	"github.com/shikejing/coverage-report-engine-go/merge"
	"github.com/shikejing/coverage-report-engine-go/report"
	"github.com/shikejing/coverage-report-engine-go/shard"
	"github.com/shikejing/coverage-report-engine-go/store"
	"github.com/shikejing/coverage-report-engine-go/threshold"
)

// Engine ties the subsystems together.
type Engine struct {
	cfg *config.Config
}

// New constructs an Engine with the given configuration.
func New(cfg *config.Config) *Engine {
	if cfg == nil {
		cfg = config.Default()
	}
	return &Engine{cfg: cfg}
}

// Config returns the active configuration.
func (e *Engine) Config() *config.Config { return e.cfg }

// Collector builds a collector bound to the configured module/source.
func (e *Engine) Collector() *collect.Collector {
	return collect.New(e.cfg.Module, e.cfg.SourceDir, covermodel.Mode(e.cfg.Mode))
}

// CollectProfile collects one profile from bytes, verifying every block
// against the local source tree. Missing, corrupt or unrecognized data is
// rejected rather than counted as zero.
func (e *Engine) CollectProfile(data []byte, sourceName, runID string) (*covermodel.Coverage, error) {
	c := e.Collector()
	return c.Collect(data, sourceName, runID)
}

// CollectFile collects a profile from a filesystem path.
func (e *Engine) CollectFile(path, runID string) (*covermodel.Coverage, error) {
	return e.Collector().CollectFile(path, runID)
}

// MergeRuns merges already collected runs in deterministic runID order.
// Each key is treated as a unique run identifier.
func (e *Engine) MergeRuns(runs map[string]*covermodel.Coverage) (*covermodel.Coverage, error) {
	return merge.Combine(covermodel.Mode(e.cfg.Mode), runs)
}

// MergeRepeated merges runs through an idempotent merger: importing the
// same runID twice is skipped and never double counts. The final result
// is independent of import order once Sort is applied (Merge does so).
func (e *Engine) MergeRepeated(runs []NamedRun) (*covermodel.Coverage, error) {
	m := merge.New(covermodel.Mode(e.cfg.Mode))
	acc := &covermodel.Coverage{Mode: covermodel.Mode(e.cfg.Mode)}
	for _, r := range runs {
		if _, err := m.Merge(acc, r.Coverage, r.ID); err != nil {
			return nil, err
		}
	}
	acc.Sort()
	return acc, nil
}

// NamedRun pairs a run identifier with its coverage.
type NamedRun struct {
	ID        string
	Coverage  *covermodel.Coverage
}

// Save stores coverage using the current format version.
func (e *Engine) Save(w io.Writer, cov *covermodel.Coverage) error {
	return store.Save(w, cov)
}

// Load reads any supported format version transparently; old entry points
// keep working when the format evolves.
func (e *Engine) Load(r io.Reader) (*covermodel.Coverage, error) {
	return store.Load(r)
}

// SaveFile stores coverage atomically.
func (e *Engine) SaveFile(path string, cov *covermodel.Coverage) error {
	return store.SaveFile(path, cov)
}

// LoadFile loads coverage from path.
func (e *Engine) LoadFile(path string) (*covermodel.Coverage, error) {
	return store.LoadFile(path)
}

// Diff computes the incremental change set relative to base.
func (e *Engine) Diff(base *covermodel.Coverage) (*incremental.ChangeSet, error) {
	return incremental.Diff(e.cfg.Module, e.cfg.SourceDir, base)
}

// RebuildIncremental reconstructs global coverage from baseline plus
// freshly collected coverage of changed files. It equals a full rebuild
// under identical input.
func (e *Engine) RebuildIncremental(base, changed *covermodel.Coverage, changes *incremental.ChangeSet) (*covermodel.Coverage, error) {
	return incremental.Rebuild(e.cfg.Module, base, changed, changes)
}

// RunShards executes concurrent shard collection with the configured
// timeout. Missing shards appear as MissingRange entries.
func (e *Engine) RunShards(ctx context.Context, specs []shard.Spec) (*shard.Outcome, error) {
	timeout := e.cfg.ShardTimeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	return shard.Run(ctx, specs, shard.Options{Timeout: timeout})
}

// Gate evaluates the configured thresholds against coverage, optionally
// comparing against a previous snapshot for regressions.
func (e *Engine) Gate(cov, previous *covermodel.Coverage) *threshold.Report {
	return threshold.Evaluate(cov, threshold.Config{
		Global:   e.cfg.Threshold,
		Packages: e.cfg.Packages,
	}, previous)
}

// WriteReport renders coverage in the named format ("json" or "text").
func (e *Engine) WriteReport(w io.Writer, cov *covermodel.Coverage, format string) error {
	switch format {
	case "json", "text":
	default:
		return fmt.Errorf("engine: unsupported report format %q", format)
	}
	return report.Write(w, cov, report.Options{Format: report.Format(format)})
}

// WriteReportWith renders coverage with explicit drill-down options.
func (e *Engine) WriteReportWith(w io.Writer, cov *covermodel.Coverage, opts report.Options) error {
	return report.Write(w, cov, opts)
}

// ReportText returns the human-readable report as a string (convenience
// for tests and short outputs; large outputs should use WriteReport).
func (e *Engine) ReportText(cov *covermodel.Coverage) (string, error) {
	var buf bytes.Buffer
	if err := e.WriteReport(&buf, cov, "text"); err != nil {
		return "", err
	}
	return buf.String(), nil
}
