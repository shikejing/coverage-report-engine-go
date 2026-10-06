// Command covengine collects, merges and reports local test coverage.
//
// Subcommands:
//
//	collect     collect one or more coverprofiles into a store
//	merge       merge stored runs into one aggregate
//	incremental rebuild coverage using a baseline plus changed profiles
//	shards      collect profiles concurrently as named shards
//	report      render a text or json report from a store
//	gate        evaluate thresholds and exit non-zero on failure
//
// All inputs come from locally executed test commands; no network access
// or credentials are required.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/shikejing/coverage-report-engine-go/config"
	"github.com/shikejing/coverage-report-engine-go/covermodel"
	"github.com/shikejing/coverage-report-engine-go/report"
	"github.com/shikejing/coverage-report-engine-go/engine"
	"github.com/shikejing/coverage-report-engine-go/shard"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	var err error
	switch cmd {
	case "collect":
		err = cmdCollect(args)
	case "merge":
		err = cmdMerge(args)
	case "incremental":
		err = cmdIncremental(args)
	case "shards":
		err = cmdShards(args)
	case "report":
		err = cmdReport(args)
	case "gate":
		err = cmdGate(args)
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n", cmd)
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "covengine:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage: covengine <command> [flags]

commands:
  collect       collect profiles into a store (rejects missing/corrupt input)
  merge         merge collected runs into one store
  incremental   rebuild global coverage from baseline + changed profiles
  shards        collect named profiles concurrently (failures marked, never silent)
  report        render text/json report with optional package/file drill-down
  gate          evaluate global/per-package thresholds`)
}

type commonFlags struct {
	configPath string
	module     string
	sourceDir  string
	mode       string
	storePath  string
}

func bindCommon(fs *flag.FlagSet) *commonFlags {
	c := &commonFlags{}
	fs.StringVar(&c.configPath, "config", "", "path to JSON config file")
	fs.StringVar(&c.module, "module", "", "module import path prefix")
	fs.StringVar(&c.sourceDir, "src", ".", "module root directory")
	fs.StringVar(&c.mode, "mode", "count", "coverage mode: set|count|atomic")
	fs.StringVar(&c.storePath, "store", "coverage.store", "coverage store path")
	return c
}

func (c *commonFlags) engine() (*engine.Engine, error) {
	cfg := config.Default()
	if c.configPath != "" {
		f, err := os.Open(c.configPath)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		cfg, err = config.Load(f)
		if err != nil {
			return nil, err
		}
	}
	if c.module != "" {
		cfg.Module = c.module
	}
	if c.sourceDir != "" {
		cfg.SourceDir = c.sourceDir
	}
	if c.mode != "" {
		cfg.Mode = c.mode
	}
	if !covermodel.Mode(cfg.Mode).Valid() {
		return nil, fmt.Errorf("unsupported mode %q", cfg.Mode)
	}
	return engine.New(cfg), nil
}

// runArg pairs a profile path with a deterministic run id.
type runArg struct {
	id   string
	path string
}

func parseRunArgs(args []string) ([]runArg, error) {
	var out []runArg
	for _, a := range args {
		parts := strings.SplitN(a, "=", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return nil, fmt.Errorf("run argument must be id=path, got %q", a)
		}
		out = append(out, runArg{id: parts[0], path: parts[1]})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("at least one id=path run argument is required")
	}
	return out, nil
}

func cmdCollect(args []string) error {
	fs := flag.NewFlagSet("collect", flag.ContinueOnError)
	c := bindCommon(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	runs, err := parseRunArgs(fs.Args())
	if err != nil {
		return err
	}
	eng, err := c.engine()
	if err != nil {
		return err
	}
	var named []engine.NamedRun
	for _, r := range runs {
		cov, err := eng.CollectFile(r.path, r.id)
		if err != nil {
			return fmt.Errorf("run %s: %w", r.id, err)
		}
		named = append(named, engine.NamedRun{ID: r.id, Coverage: cov})
	}
	merged, err := eng.MergeRepeated(named)
	if err != nil {
		return err
	}
	return eng.SaveFile(c.storePath, merged)
}

func cmdMerge(args []string) error {
	fs := flag.NewFlagSet("merge", flag.ContinueOnError)
	c := bindCommon(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	eng, err := c.engine()
	if err != nil {
		return err
	}
	cov, err := eng.LoadFile(c.storePath)
	if err != nil {
		return err
	}
	// Re-saving normalizes/sorts the store deterministically.
	return eng.SaveFile(c.storePath, cov)
}

func cmdIncremental(args []string) error {
	fs := flag.NewFlagSet("incremental", flag.ContinueOnError)
	c := bindCommon(fs)
	baseline := fs.String("baseline", "", "baseline store path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *baseline == "" {
		return fmt.Errorf("-baseline is required")
	}
	runs, err := parseRunArgs(fs.Args())
	if err != nil {
		return err
	}
	eng, err := c.engine()
	if err != nil {
		return err
	}
	base, err := eng.LoadFile(*baseline)
	if err != nil {
		return err
	}
	var named []engine.NamedRun
	for _, r := range runs {
		cov, err := eng.CollectFile(r.path, r.id)
		if err != nil {
			return fmt.Errorf("run %s: %w", r.id, err)
		}
		named = append(named, engine.NamedRun{ID: r.id, Coverage: cov})
	}
	changed, err := eng.MergeRepeated(named)
	if err != nil {
		return err
	}
	changes, err := eng.Diff(base)
	if err != nil {
		return err
	}
	rebuilt, err := eng.RebuildIncremental(base, changed, changes)
	if err != nil {
		return err
	}
	return eng.SaveFile(c.storePath, rebuilt)
}

func cmdShards(args []string) error {
	fs := flag.NewFlagSet("shards", flag.ContinueOnError)
	c := bindCommon(fs)
	timeout := fs.Duration("timeout", 0, "per-shard timeout (e.g. 30s)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	runs, err := parseRunArgs(fs.Args())
	if err != nil {
		return err
	}
	eng, err := c.engine()
	if err != nil {
		return err
	}
	specs := make([]shard.Spec, 0, len(runs))
	for _, r := range runs {
		r := r
		specs = append(specs, shard.Spec{
			Name: r.id,
			Collect: func(ctx context.Context) (*covermodel.Coverage, error) {
				// Collection itself is fast; honor ctx via a bounded read.
				return eng.CollectFile(r.path, r.id)
			},
		})
	}
	cfg := eng.Config()
	if *timeout > 0 {
		cfg.ShardTimeout = *timeout
	}
	if cfg.ShardTimeout == 0 {
		cfg.ShardTimeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	out, err := eng.RunShards(ctx, specs)
	if err != nil {
		// Still persist what was collected; missing ranges recorded.
		_ = eng.SaveFile(c.storePath, out.Coverage)
		return err
	}
	return eng.SaveFile(c.storePath, out.Coverage)
}

func cmdReport(args []string) error {
	fs := flag.NewFlagSet("report", flag.ContinueOnError)
	c := bindCommon(fs)
	format := fs.String("format", "text", "report format: text|json")
	pkg := fs.String("package", "", "drill down to one package import path")
	file := fs.String("file", "", "drill down to one package-relative file")
	blocks := fs.Bool("blocks", false, "include per-block hit detail")
	outPath := fs.String("out", "", "output file (default stdout)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	eng, err := c.engine()
	if err != nil {
		return err
	}
	cov, err := eng.LoadFile(c.storePath)
	if err != nil {
		return err
	}
	var w = os.Stdout
	if *outPath != "" {
		f, err := os.Create(*outPath)
		if err != nil {
			return err
		}
		defer f.Close()
		w = f
	}
	return eng.WriteReportWith(w, cov, reportOptions(*format, *pkg, *file, *blocks))
}

func cmdGate(args []string) error {
	fs := flag.NewFlagSet("gate", flag.ContinueOnError)
	c := bindCommon(fs)
	threshold := fs.Float64("threshold", -1, "override global minimum coverage 0..100")
	previous := fs.String("previous", "", "previous store for regression detection")
	if err := fs.Parse(args); err != nil {
		return err
	}
	eng, err := c.engine()
	if err != nil {
		return err
	}
	if *threshold >= 0 {
		eng.Config().Threshold = *threshold
	}
	cov, err := eng.LoadFile(c.storePath)
	if err != nil {
		return err
	}
	var prev *covermodel.Coverage
	if *previous != "" {
		prev, err = eng.LoadFile(*previous)
		if err != nil {
			return err
		}
	}
	rep := eng.Gate(cov, prev)
	txt, err := eng.ReportText(cov)
	if err != nil {
		return err
	}
	fmt.Print(txt)
	for _, f := range rep.Failures {
		switch f.Kind {
		case "global_below_threshold":
			fmt.Fprintf(os.Stderr, "GATE FAIL: global %.2f%% < required %.2f%%\n", f.Actual, f.Required)
		case "package_below_threshold":
			fmt.Fprintf(os.Stderr, "GATE FAIL: package %s %.2f%% < required %.2f%%\n", f.Package, f.Actual, f.Required)
		case "missing_ranges":
			fmt.Fprintln(os.Stderr, "GATE FAIL: coverage data has missing ranges; refusing to certify")
		}
	}
	for _, l := range rep.WorstUncovered {
		fmt.Fprintf(os.Stderr, "  uncovered %s/%s:%d-%d (%d stmt)\n",
			l.Package, l.File, l.StartLine, l.EndLine, l.Uncovered)
	}
	for _, r := range rep.Regressions {
		fmt.Fprintf(os.Stderr, "  regression %s/%s:%d (-%d stmt)\n",
			r.Location.Package, r.Location.File, r.Location.StartLine, r.Delta)
	}
	if !rep.Passed {
		os.Exit(3)
	}
	return nil
}

func reportOptions(format, pkg, file string, blocks bool) report.Options {
	return report.Options{
		Format:      report.Format(format),
		Package:     pkg,
		File:        file,
		BlockDetail: blocks,
	}
}
