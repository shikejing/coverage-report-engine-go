package merge

import (
	"crypto/sha256"
	"encoding/hex"
)

// RunID derives a deterministic run identifier from profile bytes when
// the caller did not supply one. Identical content always yields the same
// identifier, which makes re-importing the same run idempotent.
func RunID(data []byte) string {
	sum := sha256.Sum256(data)
	return "run-" + hex.EncodeToString(sum[:16])
}
