package cover

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestPlanShardsDeterministic(t *testing.T) {
	paths := []string{"c.go", "a.go", "b/b.go", "b/a.go", "d/e/f.go", "z.go"}
	p1 := PlanShards(paths, 3)
	p2 := PlanShards([]string{"z.go", "d/e/f.go", "b/a.go", "b/b.go", "a.go", "c.go"}, 3)
	if fmt.Sprintf("%v", p1) != fmt.Sprintf("%v", p2) {
		t.Fatalf("plan not order-independent:\n%v\n%v", p1, p2)
	}
	seen := map[string]int{}
	for _, shard := range p1.Shards {
		for _, p := range shard {
			seen[p]++
		}
	}
	if len(seen) != len(paths) {
		t.Fatalf("plan lost files: %v", seen)
	}
}

func shardProfile(runID string, files []string) *Profile {
	p := &Profile{Mode: ModeCount, RunID: runID}
	for _, f := range files {
		p.Files = append(p.Files, &FileCoverage{
			Path:   f,
			Blocks: map[string]int64{"1:1:2:1:1": 1},
			Meta:   map[string]Block{"1:1:2:1:1": {StartLine: 1, StartCol: 1, EndLine: 2, EndCol: 1, NumStmt: 1}},
		})
	}
	return p
}

func TestCollectShardsAllSucceed(t *testing.T) {
	plan := PlanShards([]string{"a.go", "b.go", "c.go", "d.go"}, 2)
	m, missing, err := CollectShards(context.Background(), plan, time.Second, 2,
		func(ctx context.Context, i int, files []string) (*Profile, error) {
			return shardProfile(fmt.Sprintf("shard-%d", i), files), nil
		})
	if err != nil || len(missing) != 0 {
		t.Fatalf("err=%v missing=%v", err, missing)
	}
	res := m.Result(missing)
	if res.Summary.Statements != 4 || res.Summary.Covered != 4 || !res.Summary.Complete {
		t.Fatalf("summary = %+v", res.Summary)
	}
}

func TestCollectShardsFailureMarksMissing(t *testing.T) {
	plan := PlanShards([]string{"a.go", "b.go", "c.go", "d.go"}, 2)
	failShard := -1
	for i, files := range plan.Shards {
		for _, f := range files {
			if f == "c.go" {
				failShard = i
			}
		}
	}
	m, missing, err := CollectShards(context.Background(), plan, time.Second, 2,
		func(ctx context.Context, i int, files []string) (*Profile, error) {
			if i == failShard {
				return nil, errors.New("boom")
			}
			return shardProfile(fmt.Sprintf("shard-%d", i), files), nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) == 0 {
		t.Fatal("want missing ranges")
	}
	for _, mr := range missing {
		if mr.Reason != string(ReasonShardFailed) {
			t.Fatalf("reason = %q", mr.Reason)
		}
	}
	res := m.Result(missing)
	if res.Summary.Complete {
		t.Fatal("result must be marked incomplete")
	}
	// Failed shard's files are excluded, not zero-hit.
	for _, ps := range res.Packages {
		for _, fs := range ps.Files {
			for _, mr := range missing {
				if fs.Path == mr.Path {
					t.Fatalf("missing file %s leaked into stats", fs.Path)
				}
			}
		}
	}
}

func TestCollectShardsTimeout(t *testing.T) {
	plan := PlanShards([]string{"a.go", "b.go"}, 2)
	m, missing, err := CollectShards(context.Background(), plan, 20*time.Millisecond, 2,
		func(ctx context.Context, i int, files []string) (*Profile, error) {
			if i%2 == 0 {
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(time.Second):
				}
			}
			return shardProfile(fmt.Sprintf("shard-%d", i), files), nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) == 0 {
		t.Fatal("want missing ranges for timed-out shard")
	}
	found := false
	for _, mr := range missing {
		if mr.Reason == string(ReasonShardTimeout) {
			found = true
		}
	}
	if !found {
		t.Fatalf("want timeout reason, got %v", missing)
	}
	_ = m
}

func TestCollectShardsCancelLeavesNoPartialState(t *testing.T) {
	plan := PlanShards([]string{"a.go", "b.go", "c.go"}, 3)
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	var once sync.Once
	m, _, err := CollectShards(ctx, plan, 0, 1,
		func(ctx context.Context, i int, files []string) (*Profile, error) {
			once.Do(func() { close(started) })
			cancel() // cancel mid-flight
			<-ctx.Done()
			return nil, ctx.Err()
		})
	<-started
	if err == nil {
		t.Fatal("want cancellation error")
	}
	var rej *RejectError
	if !errors.As(err, &rej) || rej.Reason != ReasonCanceled {
		t.Fatalf("want canceled reason, got %v", err)
	}
	if m != nil {
		t.Fatal("merger must be nil on cancel: no half-merged state")
	}
}
