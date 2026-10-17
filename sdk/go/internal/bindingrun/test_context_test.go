package bindingrun

import (
	"context"
	"testing"
)

// testContext uses Go 1.23 APIs so canonical runner tests also work in the SDK.
func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return ctx
}
