// Package shard collects multiple coverage shards concurrently. A
// shard failure, timeout or cancellation never silently lowers the
// overall coverage: the affected range is recorded as a MissingRange in
// the otherwise complete aggregate. Results are only merged after every
// shard has settled, so cancellation cannot leave a half-merged state.
package shard

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/shikejing/coverage-report-engine-go/covermodel"
)

// Spec is one independently collected shard.
type Spec struct {
	// Name is the deterministic identifier used in MissingRange records
	// and merged run IDs.
	Name string
	// Collect produces the shard coverage. It must respect ctx so
	// cancellation and timeouts can take effect promptly.
	Collect func(ctx context.Context) (*covermodel.Coverage, error)
}

// Options controls concurrent collection.
type Options struct {
	// Timeout bounds each individual shard. Zero means no per-shard
	// timeout (the caller's context still applies).
	Timeout time.Duration
}

// Outcome is the aggregate result of a shard run.
type Outcome struct {
	Coverage *covermodel.Coverage
	// Missing lists every shard that did not deliver coverage, with a
	// deterministic kind: "failed", "timeout" or "canceled".
	Missing []covermodel.MissingRange
}

// AggregateError is returned (wrapping context errors where relevant)
// only when the run as a whole cannot proceed; individual shard failures
// are reported through Outcome.Missing instead.
type AggregateError struct {
	Failed  []covermodel.MissingRange
	Context error
}

func (e *AggregateError) Error() string {
	return fmt.Sprintf("shard: collection incomplete: %d shard(s) missing; context: %v",
		len(e.Failed), e.Context)
}

func (e *AggregateError) Unwrap() error { return e.Context }

type shardResult struct {
	name string
	cov  *covermodel.Coverage
	mr   covermodel.MissingRange
	ok   bool
}

// Run executes all shards concurrently. Successful shards are merged in
// deterministic (name) order with each shard tagged by its run ID; failed
// shards appear in Missing instead of contributing zero coverage.
//
// The returned error is non-nil only when the caller canceled ctx (the
// aggregate up to that point is still returned, fully merged).
func Run(ctx context.Context, specs []Spec, opts Options) (*Outcome, error) {
	results := make([]shardResult, len(specs))
	var wg sync.WaitGroup
	for i, spec := range specs {
		wg.Add(1)
		go func(i int, spec Spec) {
			defer wg.Done()
			results[i] = runOne(ctx, spec, opts.Timeout)
		}(i, spec)
	}
	wg.Wait()

	// Merge phase runs strictly after all shards have settled, so no
	// partial aggregate is ever observable.
	mode := firstMode(results)
	merger := newAssembler(mode)
	out := &Outcome{Coverage: &covermodel.Coverage{Mode: mode}}
	sort.SliceStable(results, func(i, j int) bool { return results[i].name < results[j].name })

	var ctxErr error
	for _, r := range results {
		if r.ok {
			merger.add(r.cov, r.name)
			continue
		}
		out.Missing = append(out.Missing, r.mr)
		if r.mr.Kind == "canceled" {
			ctxErr = ctx.Err()
		}
	}
	out.Coverage = merger.finish(out.Missing)

	if ctxErr != nil {
		return out, &AggregateError{Failed: append([]covermodel.MissingRange(nil), out.Missing...), Context: ctxErr}
	}
	return out, nil
}

func runOne(parent context.Context, spec Spec, timeout time.Duration) shardResult {
	ctx := parent
	var cancel context.CancelFunc
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(parent, timeout)
	} else {
		ctx, cancel = context.WithCancel(parent)
	}
	defer cancel()

	done := make(chan shardResult, 1)
	go func() {
		cov, err := spec.Collect(ctx)
		if err != nil {
			kind := "failed"
			detail := err.Error()
			if ctx.Err() == context.DeadlineExceeded || isTimeout(err) {
				if parent.Err() == context.Canceled {
					kind = "canceled"
				} else {
					kind = "timeout"
				}
			} else if parent.Err() == context.Canceled {
				kind = "canceled"
			}
			done <- shardResult{
				name: spec.Name,
				mr:   covermodel.MissingRange{Shard: spec.Name, Kind: kind, Detail: detail},
			}
			return
		}
		done <- shardResult{name: spec.Name, cov: cov, ok: true}
	}()

	select {
	case res := <-done:
		return res
	case <-ctx.Done():
		// Prefer the parent's signal: its cancellation overrides a
		// shard-local deadline.
		kind := "timeout"
		if parent.Err() == context.Canceled {
			kind = "canceled"
		} else if ctx.Err() == context.Canceled {
			kind = "canceled"
		}
		detail := ctx.Err().Error()
		if parent.Err() != nil {
			detail = parent.Err().Error()
		}
		return shardResult{
			name: spec.Name,
			mr:   covermodel.MissingRange{Shard: spec.Name, Kind: kind, Detail: detail},
		}
	}
}

func isTimeout(err error) bool {
	type timeoutErr interface{ Timeout() bool }
	te, ok := err.(timeoutErr)
	return ok && te.Timeout()
}

func firstMode(results []shardResult) covermodel.Mode {
	for _, r := range results {
		if r.ok && r.cov != nil && r.cov.Mode.Valid() {
			return r.cov.Mode
		}
	}
	return covermodel.ModeCount
}

// assembler merges successful shards using the model's deterministic block
// semantics without importing the merge package's run dedup map (shards
// are distinct by construction).
type assembler struct {
	mode covermodel.Mode
	cov  *covermodel.Coverage
}

func newAssembler(mode covermodel.Mode) *assembler {
	if !mode.Valid() {
		mode = covermodel.ModeCount
	}
	return &assembler{mode: mode, cov: &covermodel.Coverage{Mode: mode}}
}

func (a *assembler) add(src *covermodel.Coverage, name string) {
	if src == nil {
		return
	}
	for _, sp := range src.Packages {
		dp := a.cov.Package(sp.ImportPath)
		if dp == nil {
			dp = &covermodel.Package{ImportPath: sp.ImportPath}
			a.cov.Packages = append(a.cov.Packages, dp)
		}
		for _, sf := range sp.Files {
			var df *covermodel.File
			for _, f := range dp.Files {
				if f.Path == sf.Path {
					df = f
					break
				}
			}
			if df == nil {
				df = &covermodel.File{Path: sf.Path, Hash: sf.Hash}
				dp.Files = append(dp.Files, df)
			}
			a.mergeBlocks(df, sf.Blocks)
		}
	}
	a.cov.RunIDs = append(a.cov.RunIDs, name)
}

type bkey struct{ sl, sc, el, ec, ns int }

func (a *assembler) mergeBlocks(df *covermodel.File, blocks []covermodel.Block) {
	idx := make(map[bkey]int, len(df.Blocks))
	for i, b := range df.Blocks {
		idx[bkey{b.StartLine, b.StartCol, b.EndLine, b.EndCol, b.NumStmt}] = i
	}
	for _, b := range blocks {
		k := bkey{b.StartLine, b.StartCol, b.EndLine, b.EndCol, b.NumStmt}
		if i, ok := idx[k]; ok {
			if a.mode == covermodel.ModeSet {
				if b.Count > 0 || df.Blocks[i].Count > 0 {
					df.Blocks[i].Count = 1
				}
			} else {
				df.Blocks[i].Count += b.Count
			}
			continue
		}
		idx[k] = len(df.Blocks)
		df.Blocks = append(df.Blocks, b)
	}
}

func (a *assembler) finish(missing []covermodel.MissingRange) *covermodel.Coverage {
	a.cov.Missing = append(a.cov.Missing, missing...)
	a.cov.Sort()
	return a.cov
}
