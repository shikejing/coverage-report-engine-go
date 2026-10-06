// Package collect parses and validates coverage profiles produced by
// locally executed test commands.
package collect

import (
	"errors"
	"fmt"
)

// RejectKind classifies why a coverage input was refused. Distinct kinds
// let callers react differently to each data quality problem instead of
// treating them all as zero coverage.
type RejectKind string

// Distinguishable rejection reasons.
const (
	// KindMissing means the profile did not exist or could not be read.
	KindMissing RejectKind = "missing"
	// KindCorrupt means the profile existed but was malformed.
	KindCorrupt RejectKind = "corrupt"
	// KindUnknownSource means a block references a source file that is not
	// present in the local module being measured.
	KindUnknownSource RejectKind = "unknown_source"
	// KindModeMismatch means the profile declares a different counter mode
	// than the run it is being merged with.
	KindModeMismatch RejectKind = "mode_mismatch"
)

// RejectError is returned for coverage data that must not be counted as
// zero hits.
type RejectError struct {
	Kind   RejectKind
	Source string
	Line   int
	Reason string
	Err    error
}

func (e *RejectError) Error() string {
	loc := e.Source
	if e.Line > 0 {
		loc = fmt.Sprintf("%s:%d", e.Source, e.Line)
	}
	msg := string(e.Kind)
	if loc != "" {
		msg += ": " + loc
	}
	if e.Reason != "" {
		msg += ": " + e.Reason
	}
	if e.Err != nil {
		if msg != "" {
			msg += ": "
		}
		msg += e.Err.Error()
	}
	return msg
}

func (e *RejectError) Unwrap() error { return e.Err }

// AsRejectError extracts a *RejectError from err.
func AsRejectError(err error) (*RejectError, bool) {
	var re *RejectError
	if errors.As(err, &re) {
		return re, true
	}
	return nil, false
}
