package cover

import (
	"fmt"
	"sort"
)

// Thresholds configures coverage gates.
type Thresholds struct {
	Overall  float64            `json:"overall"`            // required global percent
	Packages map[string]float64 `json:"packages,omitempty"` // per-package overrides
	TopN     int                `json:"topN,omitempty"`     // worst locations to list
}

// Violation is one threshold failure.
type Violation struct {
	Scope    string  `json:"scope"` // "overall" or package path
	Required float64 `json:"required"`
	Actual   float64 `json:"actual"`
}

// GateError reports threshold failures with distinguishable scopes.
type GateError struct {
	Violations []Violation
	Worst      []WorstLocation
}

func (e *GateError) Error() string {
	return fmt.Sprintf("cover: %d threshold violation(s)", len(e.Violations))
}

// WorstLocation pinpoints a file with low coverage or a regression.
type WorstLocation struct {
	Path      string  `json:"path"`
	Percent   float64 `json:"percent"`
	Delta     float64 `json:"delta"` // vs baseline, negative = regression
	Uncovered int     `json:"uncovered"`
}

// Check evaluates the result against thresholds deterministically.
// baseline may be nil; when present, per-file regressions are reported in
// Worst. Output ordering is fully determined by the inputs.
func Check(res *Result, baseline *Result, th Thresholds) *GateError {
	gate := &GateError{}

	if res.Summary.Percent+1e-9 < th.Overall {
		gate.Violations = append(gate.Violations, Violation{
			Scope: "overall", Required: th.Overall, Actual: res.Summary.Percent,
		})
	}
	// Per-package checks in sorted package order (res.Packages is sorted).
	for _, ps := range res.Packages {
		req, ok := th.Packages[ps.Path]
		if ok && ps.Percent+1e-9 < req {
			gate.Violations = append(gate.Violations, Violation{
				Scope: "package:" + ps.Path, Required: req, Actual: ps.Percent,
			})
		}
	}

	// Worst locations: rank by (uncovered statements desc, regression
	// asc, path asc) so ties break deterministically.
	basePct := map[string]float64{}
	if baseline != nil {
		for _, ps := range baseline.Packages {
			for _, fs := range ps.Files {
				basePct[fs.Path] = fs.Percent
			}
		}
	}
	var worst []WorstLocation
	for _, ps := range res.Packages {
		for _, fs := range ps.Files {
			wl := WorstLocation{
				Path:      fs.Path,
				Percent:   fs.Percent,
				Uncovered: fs.Statements - fs.Covered,
			}
			if baseline != nil {
				wl.Delta = fs.Percent - basePct[fs.Path]
			}
			worst = append(worst, wl)
		}
	}
	sort.Slice(worst, func(i, j int) bool {
		a, b := worst[i], worst[j]
		if a.Uncovered != b.Uncovered {
			return a.Uncovered > b.Uncovered
		}
		if a.Delta != b.Delta {
			return a.Delta < b.Delta
		}
		return a.Path < b.Path
	})
	n := th.TopN
	if n <= 0 {
		n = 5
	}
	if len(worst) > n {
		worst = worst[:n]
	}
	gate.Worst = worst

	if len(gate.Violations) == 0 {
		return nil
	}
	return gate
}
