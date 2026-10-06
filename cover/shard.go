package cover

import (
	"context"
	"hash/fnv"
	"sort"
	"sync"
	"time"
)

// ShardPlan deterministically assigns files to shards.
type ShardPlan struct {
	Shards [][]string // shard index -> sorted file paths
}

// PlanShards splits paths into n deterministic shards: paths are sorted,
// then assigned by FNV hash modulo n, then each shard is sorted. The same
// input always yields the same plan.
func PlanShards(paths []string, n int) ShardPlan {
	if n < 1 {
		n = 1
	}
	sorted := append([]string(nil), paths...)
	sort.Strings(sorted)
	plan := ShardPlan{Shards: make([][]string, n)}
	for _, p := range sorted {
		h := fnv.New32a()
		h.Write([]byte(p))
		i := int(h.Sum32() % uint32(n))
		plan.Shards[i] = append(plan.Shards[i], p)
	}
	for i := range plan.Shards {
		sort.Strings(plan.Shards[i])
	}
	return plan
}

// ShardResult is the outcome of one shard.
type ShardResult struct {
	Index   int
	Profile *Profile
	Missing []MissingRange
	Err     error
}

// CollectFunc gathers coverage for the files of one shard. It must honor
// ctx cancellation.
type CollectFunc func(ctx context.Context, shardIndex int, files []string) (*Profile, error)

// CollectShards runs fn for every shard concurrently, bounded by
// maxParallel and per-shard timeout. A failed or timed-out shard yields
// deterministic MissingRange entries instead of silently lowering
// coverage. Profiles are merged only after every shard has finished, so
// cancellation or timeout never leaves a half-merged Merger: on ctx
// cancellation the returned Merger is nil and the error carries
// ReasonCanceled.
func CollectShards(ctx context.Context, plan ShardPlan, timeout time.Duration, maxParallel int, fn CollectFunc) (*Merger, []MissingRange, error) {
	if maxParallel < 1 {
		maxParallel = 1
	}
	results := make([]ShardResult, len(plan.Shards))
	sem := make(chan struct{}, maxParallel)
	var wg sync.WaitGroup

	for i, files := range plan.Shards {
		i, files := i, files
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				results[i] = ShardResult{Index: i, Err: reject(ReasonCanceled, "shard %d not started: %v", i, ctx.Err())}
				return
			}
			sctx := ctx
			cancel := func() {}
			if timeout > 0 {
				sctx, cancel = context.WithTimeout(ctx, timeout)
			}
			defer cancel()
			prof, err := fn(sctx, i, files)
			switch {
			case err == nil:
				results[i] = ShardResult{Index: i, Profile: prof}
			case sctx.Err() == context.DeadlineExceeded:
				results[i] = ShardResult{Index: i, Err: reject(ReasonShardTimeout, "shard %d exceeded %s", i, timeout)}
			case ctx.Err() != nil:
				results[i] = ShardResult{Index: i, Err: reject(ReasonCanceled, "shard %d canceled: %v", i, ctx.Err())}
			default:
				results[i] = ShardResult{Index: i, Err: reject(ReasonShardFailed, "shard %d: %v", i, err)}
			}
		}()
	}
	wg.Wait()

	// Whole-operation cancellation: discard everything, merge nothing.
	if err := ctx.Err(); err != nil {
		return nil, nil, reject(ReasonCanceled, "collection canceled: %v", err)
	}

	// Merge only after all shards settled; results are processed in shard
	// index order so the outcome is deterministic.
	m := NewMerger()
	var missing []MissingRange
	for i, res := range results {
		if res.Err != nil {
			rej, ok := res.Err.(*RejectError)
			reason := string(ReasonShardFailed)
			if ok {
				reason = string(rej.Reason)
			}
			for _, f := range plan.Shards[i] {
				missing = append(missing, MissingRange{Path: f, StartLine: 1, EndLine: -1, Reason: reason})
			}
			continue
		}
		if res.Profile == nil {
			continue
		}
		if _, err := m.Import(res.Profile); err != nil {
			return nil, nil, err
		}
	}
	return m, missing, nil
}
