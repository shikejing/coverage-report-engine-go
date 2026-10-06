package cover

import "fmt"

// RejectReason classifies why an input was rejected, so callers can
// distinguish corrupt data from unknown provenance from version skew.
type RejectReason string

const (
	ReasonMissingMode     RejectReason = "missing_mode_header"
	ReasonUnknownMode     RejectReason = "unknown_mode"
	ReasonMalformedLine   RejectReason = "malformed_line"
	ReasonUnknownSource   RejectReason = "unknown_source"
	ReasonDupBlockGeom    RejectReason = "conflicting_block_geometry"
	ReasonBadMagic        RejectReason = "bad_magic"
	ReasonIncompatVersion RejectReason = "incompatible_version"
	ReasonCorruptPayload  RejectReason = "corrupt_payload"
	ReasonShardFailed     RejectReason = "shard_failed"
	ReasonShardTimeout    RejectReason = "shard_timeout"
	ReasonCanceled        RejectReason = "canceled"
)

// RejectError is a typed rejection with a machine-distinguishable reason.
type RejectError struct {
	Reason RejectReason
	Detail string
}

func (e *RejectError) Error() string {
	return fmt.Sprintf("cover: rejected (%s): %s", e.Reason, e.Detail)
}

func reject(reason RejectReason, format string, args ...any) *RejectError {
	return &RejectError{Reason: reason, Detail: fmt.Sprintf(format, args...)}
}
