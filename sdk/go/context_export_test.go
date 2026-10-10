package faas

import (
	"context"
	"testing"
)

// SDKTestContext keeps request cleanup compatible with the SDK's Go 1.23 minimum.
// It is exported only in the test binary for the external transport tests.
func SDKTestContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return ctx
}
