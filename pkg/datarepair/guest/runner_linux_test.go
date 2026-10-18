//go:build linux

package guest

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	core "github.com/onebox-faas/faas/pkg/datarepair"
)

// The portable gate copies this static test binary into its networkless PG16
// container and sends exactly the executor's snapshot over stdin.
func TestRehearsalGuestProcess(t *testing.T) {
	if os.Getenv("GREGALE_REPAIR_GUEST_HELPER") != "1" {
		t.Skip("subprocess helper")
	}
	in, err := core.ReadRehearsalBody(os.Stdin)
	if err != nil {
		os.Exit(2)
	}
	dir, err := os.MkdirTemp("/tmp", "gregale-repair-guest-")
	if err != nil {
		os.Exit(2)
	}
	result := Run(context.Background(), in, dir)
	_ = os.RemoveAll(dir)
	_ = json.NewEncoder(os.Stdout).Encode(result)
	os.Exit(0)
}
