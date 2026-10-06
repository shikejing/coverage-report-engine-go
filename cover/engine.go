package cover

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Config drives an Engine run.
type Config struct {
	Root         string // module root; profile paths must live under it
	ShardCount   int    // number of deterministic shards
	MaxParallel  int    // concurrent shard collectors
	ShardTimeout time.Duration
	Thresholds   Thresholds
}

// Engine orchestrates collection, merge, thresholds and reporting.
type Engine struct {
	cfg Config
}

// NewEngine creates an engine with the given config.
func NewEngine(cfg Config) *Engine {
	if cfg.ShardCount < 1 {
		cfg.ShardCount = 1
	}
	if cfg.MaxParallel < 1 {
		cfg.MaxParallel = 1
	}
	return &Engine{cfg: cfg}
}

// Ingest parses and merges one profile stream into m. It returns
// (imported, error); a duplicate RunID yields (false, nil).
func (e *Engine) Ingest(m *Merger, r io.Reader, runID string) (bool, error) {
	p, err := ParseProfile(r, e.cfg.Root, runID)
	if err != nil {
		return false, err
	}
	return m.Import(p)
}

// CollectShards parses one profile per shard concurrently and merges
// them. profiles[i] is the raw profile stream for shard i; a nil entry
// marks that shard as failed (its files become deterministic missing
// ranges). Cancellation leaves no partial state in the returned merger.
func (e *Engine) CollectShards(ctx context.Context, streams map[int]io.Reader, plan ShardPlan) (*Merger, []MissingRange, error) {
	fn := func(ctx context.Context, idx int, files []string) (*Profile, error) {
		r, ok := streams[idx]
		if !ok || r == nil {
			return nil, reject(ReasonShardFailed, "no input stream for shard %d", idx)
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return ParseProfile(r, e.cfg.Root, fmt.Sprintf("shard-%d", idx))
	}
	return CollectShards(ctx, plan, e.cfg.ShardTimeout, e.cfg.MaxParallel, fn)
}

// HashFile computes the content hash used for incremental change
// detection. It streams the file so memory stays bounded.
func HashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ListGoFiles returns the sorted set of .go source files under root,
// relative to root, skipping vendor and testdata directories.
func ListGoFiles(root string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case "vendor", "testdata", ".git":
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(d.Name(), ".go") {
			rel, err := filepath.Rel(root, p)
			if err != nil {
				return err
			}
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}
