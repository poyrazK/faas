package gateway

import "errors"

// ErrReleaseGone is the durable resolver's not-found/expired verdict.
var ErrReleaseGone = errors.New("project release is unavailable")

// ErrReleaseConflict means the graph is incomplete or the caller deployment
// belongs to more than one unexpired set without an explicit release header.
var ErrReleaseConflict = errors.New("project release is ambiguous or incomplete")
