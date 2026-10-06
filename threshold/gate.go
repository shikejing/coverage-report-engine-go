// Package threshold evaluates deterministic coverage gates.
package threshold

import (
	"sort"

	"github.com/shikejing/coverage-report-engine-go/covermodel"
)

// Config declares the global and per-package minimum statement coverage
// (0..100).
type Config struct {
	Global   float64
	Packages map[string]float64
}

// FailureKind distinguishes gate failure reasons.
type FailureKind string

// Failure kinds.
const (
	FailGlobal        FailureKind = "global_below_threshold"
	FailPackage       FailureKind = "package_below_threshold"
	FailMissingRanges FailureKind = "missing_ranges"
)

// Location identifies an uncovered executable range. Uncovered counts the
// statements in the range; for covered ranges used in regression listing
// it is 0.
type Location struct {
	Package   string
	File      string
	StartLine int
	EndLine   int
	NumStmt   int
	Uncovered int
}

// Regression is a range that was covered before but is uncovered now.
type Regression struct {
	Location Location
	Delta    int // newly uncovered statements
}

// Failure describes one violated gate.
type Failure struct {
	Kind     FailureKind
	Package  string
	Actual   float64
	Required float64
}

// Report is the deterministic gate result.
type Report struct {
	Passed         bool
	Percent        float64
	Failures       []Failure
	WorstUncovered []Location
	Regressions    []Regression
}

const defaultWorstLimit = 10

// Evaluate checks coverage against thresholds. A previous snapshot may be
// supplied to compute regressions; pass nil to skip that check.
//
// Ordering and threshold handling are fully deterministic: equal inputs
// always produce an equal report.
func Evaluate(cov *covermodel.Coverage, cfg Config, previous *covermodel.Coverage) *Report {
	rep := &Report{Percent: cov.Percent()}

	// 1) Missing ranges are always a distinct, explicit failure.
	if len(cov.Missing) > 0 {
		rep.Failures = append(rep.Failures, Failure{
			Kind:   FailMissingRanges,
			Actual: rep.Percent,
		})
	}

	// 2) Per-package thresholds, evaluated in import-path order.
	pkgNames := make([]string, 0, len(cov.Packages))
	for _, p := range cov.Packages {
		pkgNames = append(pkgNames, p.ImportPath)
	}
	sort.Strings(pkgNames)
	for _, name := range pkgNames {
		required, ok := cfg.Packages[name]
		if !ok {
			continue
		}
		pkg := cov.Package(name)
		actual := packagePercent(pkg)
		if actual < required {
			rep.Failures = append(rep.Failures, Failure{
				Kind:     FailPackage,
				Package:  name,
				Actual:   actual,
				Required: required,
			})
		}
	}

	// 3) Global threshold.
	if rep.Percent < cfg.Global {
		rep.Failures = append(rep.Failures, Failure{
			Kind:     FailGlobal,
			Actual:   rep.Percent,
			Required: cfg.Global,
		})
	}

	rep.Passed = len(rep.Failures) == 0
	rep.WorstUncovered = worstUncovered(cov, defaultWorstLimit)
	if previous != nil {
		rep.Regressions = findRegressions(previous, cov, defaultWorstLimit)
	}
	return rep
}

func packagePercent(pkg *covermodel.Package) float64 {
	total, covered := 0, 0
	for _, f := range pkg.Files {
		for _, b := range f.Blocks {
			total += b.NumStmt
			if b.Count > 0 {
				covered += b.NumStmt
			}
		}
	}
	if total == 0 {
		return 100
	}
	return float64(covered) * 100 / float64(total)
}

// worstUncovered lists uncovered ranges with the most statements first.
// Ties break by package, file and line so output is reproducible.
func worstUncovered(cov *covermodel.Coverage, limit int) []Location {
	var locs []Location
	for _, pkg := range cov.Packages {
		for _, f := range pkg.Files {
			for _, b := range f.Blocks {
				if b.Count == 0 && b.NumStmt > 0 {
					locs = append(locs, Location{
						Package:   pkg.ImportPath,
						File:      f.Path,
						StartLine: b.StartLine,
						EndLine:   b.EndLine,
						NumStmt:   b.NumStmt,
						Uncovered: b.NumStmt,
					})
				}
			}
		}
	}
	sortLocations(locs)
	if len(locs) > limit {
		locs = locs[:limit]
	}
	return locs
}

// findRegressions compares previous against current and returns ranges
// that lost coverage, with the largest regressions first.
func findRegressions(prev, cur *covermodel.Coverage, limit int) []Regression {
	type pos struct {
		pkg, file          string
		sl, sc, el, ec, ns int
	}
	now := make(map[pos]int64)
	for _, pkg := range cur.Packages {
		for _, f := range pkg.Files {
			for _, b := range f.Blocks {
				now[pos{pkg.ImportPath, f.Path, b.StartLine, b.StartCol, b.EndLine, b.EndCol, b.NumStmt}] = b.Count
			}
		}
	}
	var regs []Regression
	for _, pkg := range prev.Packages {
		for _, f := range prev.Package(pkg.ImportPath).Files {
			for _, b := range f.Blocks {
				if b.Count == 0 {
					continue
				}
				k := pos{pkg.ImportPath, f.Path, b.StartLine, b.StartCol, b.EndLine, b.EndCol, b.NumStmt}
				if c, ok := now[k]; ok && c == 0 {
					regs = append(regs, Regression{
						Location: Location{
							Package:   pkg.ImportPath,
							File:      f.Path,
							StartLine: b.StartLine,
							EndLine:   b.EndLine,
							NumStmt:   b.NumStmt,
							Uncovered: b.NumStmt,
						},
						Delta: b.NumStmt,
					})
				}
			}
		}
	}
	sortRegressions(regs)
	if len(regs) > limit {
		regs = regs[:limit]
	}
	return regs
}

func sortLocations(locs []Location) {
	sort.SliceStable(locs, func(i, j int) bool {
		if locs[i].Uncovered != locs[j].Uncovered {
			return locs[i].Uncovered > locs[j].Uncovered
		}
		if locs[i].Package != locs[j].Package {
			return locs[i].Package < locs[j].Package
		}
		if locs[i].File != locs[j].File {
			return locs[i].File < locs[j].File
		}
		return locs[i].StartLine < locs[j].StartLine
	})
}

func sortRegressions(regs []Regression) {
	sort.SliceStable(regs, func(i, j int) bool {
		if regs[i].Delta != regs[j].Delta {
			return regs[i].Delta > regs[j].Delta
		}
		a, b := regs[i].Location, regs[j].Location
		if a.Package != b.Package {
			return a.Package < b.Package
		}
		if a.File != b.File {
			return a.File < b.File
		}
		return a.StartLine < b.StartLine
	})
}
