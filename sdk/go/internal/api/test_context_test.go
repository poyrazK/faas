package api

import (
	"context"
	"testing"
)

// testContext cancels work during cleanup using the SDK's Go 1.23 API floor.
func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return ctx
}
