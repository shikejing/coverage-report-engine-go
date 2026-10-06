package store

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/shikejing/coverage-report-engine-go/covermodel"
)

// SaveFile writes coverage to path atomically: data is first written to a
// sibling temp file and renamed into place, so canceled or crashed writes
// never leave a half-written store behind.
func SaveFile(path string, c *covermodel.Coverage) error {
	dir := filepath.Dir(path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("coverage store: %w", err)
		}
	}
	tmp, err := os.CreateTemp(dir, ".covstore-*")
	if err != nil {
		return fmt.Errorf("coverage store: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			os.Remove(tmpName)
		}
	}()
	if err := Save(tmp, c); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("coverage store: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("coverage store: %w", err)
	}
	cleanup = false
	return nil
}

// LoadFile reads coverage from a file. A missing file yields a
// *VersionError so callers can distinguish absence from corruption.
func LoadFile(path string) (*covermodel.Coverage, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, &VersionError{
				Min:    MinSupportedVersion,
				Max:    CurrentVersion,
				Reason: "no stored coverage at " + path,
			}
		}
		return nil, fmt.Errorf("coverage store: %w", err)
	}
	defer f.Close()
	return Load(f)
}
