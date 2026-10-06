// Package config loads engine configuration. Configuration is JSON so
// the same checked-in file yields identical behavior everywhere.
package config

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/shikejing/coverage-report-engine-go/covermodel"
)

// ReportConfig configures report generation.
type ReportConfig struct {
	// Formats lists report formats to emit: "json" and/or "text".
	Formats []string `json:"formats"`
	// OutDir is the directory reports are written into.
	OutDir string `json:"outDir"`
}

// Config is the root configuration.
type Config struct {
	// Module is the module import path prefix.
	Module string `json:"module"`
	// SourceDir is the module root on the local filesystem.
	SourceDir string `json:"sourceDir"`
	// Mode pins the coverage mode ("set", "count", "atomic").
	Mode string `json:"mode"`
	// Threshold is the global minimum statement coverage (0..100).
	Threshold float64 `json:"threshold"`
	// Packages pins per-package minimum coverage.
	Packages map[string]float64 `json:"packages"`
	// ShardTimeout bounds each shard ("30s"); zero disables it.
	ShardTimeout time.Duration `json:"shardTimeout"`
	// Report configures report rendering.
	Report ReportConfig `json:"report"`
}

// Default returns the built-in configuration.
func Default() *Config {
	return &Config{
		Mode:      string(covermodel.ModeCount),
		Threshold: 0,
		Report:    ReportConfig{Formats: []string{"text"}},
	}
}

// rawConfig carries the string-encoded shard timeout from JSON.
type rawConfig struct {
	Module       string             `json:"module"`
	SourceDir    string             `json:"sourceDir"`
	Mode         string             `json:"mode"`
	Threshold    float64            `json:"threshold"`
	Packages     map[string]float64 `json:"packages"`
	ShardTimeout string             `json:"shardTimeout"`
	Report       ReportConfig       `json:"report"`
}

// Load reads JSON configuration.
func Load(r io.Reader) (*Config, error) {
	var raw rawConfig
	dec := json.NewDecoder(r)
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	cfg := &Config{
		Module:    strings.TrimSpace(raw.Module),
		SourceDir: raw.SourceDir,
		Mode:      raw.Mode,
		Threshold: raw.Threshold,
		Packages:  raw.Packages,
		Report:    raw.Report,
	}
	if cfg.Mode == "" {
		cfg.Mode = string(covermodel.ModeCount)
	}
	if !covermodel.Mode(cfg.Mode).Valid() {
		return nil, fmt.Errorf("config: unsupported mode %q", cfg.Mode)
	}
	if raw.ShardTimeout != "" {
		d, err := time.ParseDuration(raw.ShardTimeout)
		if err != nil {
			return nil, fmt.Errorf("config: invalid shardTimeout: %w", err)
		}
		cfg.ShardTimeout = d
	}
	if cfg.Threshold < 0 || cfg.Threshold > 100 {
		return nil, fmt.Errorf("config: threshold must be within 0..100, got %v", cfg.Threshold)
	}
	for pkg, v := range cfg.Packages {
		if v < 0 || v > 100 {
			return nil, fmt.Errorf("config: package threshold for %q must be within 0..100, got %v", pkg, v)
		}
	}
	if len(cfg.Report.Formats) == 0 {
		cfg.Report.Formats = []string{"text"}
	}
	for _, f := range cfg.Report.Formats {
		switch f {
		case "json", "text":
		default:
			return nil, fmt.Errorf("config: unsupported report format %q", f)
		}
	}
	return cfg, nil
}
