//go:build !metal || !linux

package fcvm

import "context"

// Acceptance barriers are absent from normal production binaries.
func executionTemplateCaptureCheckpoint(context.Context, string, string, string) error { return nil }
