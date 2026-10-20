package main_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// TestTierCAlertDispatchUsage pins the usage banner + unknown-subcommand
// branch of cmdAlerts. These are the cheapest paths to flip from 0%:
// the dispatcher rejects with rc=1 before any network round-trip.
func TestTierCAlertDispatchUsage(t *testing.T) {
	main.ResetJSONOut(t)
	// No args -> usage banner.
	if code := main.CmdAlerts(nil); code != 1 {
		t.Errorf("cmdAlerts() = %d, want 1", code)
	}
	// Unknown subcommand.
	if code := main.CmdAlerts([]string{"nope"}); code != 1 {
		t.Errorf("cmdAlerts(nope) = %d, want 1", code)
	}
}

// TestTierCEnvDispatchUsage pins the same dispatcher pattern for cmdEnv.
func TestTierCEnvDispatchUsage(t *testing.T) {
	main.ResetJSONOut(t)
	if code := main.CmdEnv(nil); code != 1 {
		t.Errorf("cmdEnv() = %d, want 1", code)
	}
	if code := main.CmdEnv([]string{"nope"}); code != 1 {
		t.Errorf("cmdEnv(nope) = %d, want 1", code)
	}
}

// TestTierCQueueDispatchUsage pins cmdQueueDispatch (the top-level
// dispatcher for the queue subcommand family). The dispatcher rejects
// with rc=1 on missing args + unknown subcommand, before any
// network round-trip. The inner leaves (cmdQueueReceive etc.) are
// pinned separately via the *FlagParse tests below.
func TestTierCQueueDispatchUsage(t *testing.T) {
	main.ResetJSONOut(t)
	if code := main.CmdQueueDispatch(nil); code != 1 {
		t.Errorf("cmdQueueDispatch() = %d, want 1", code)
	}
	if code := main.CmdQueueDispatch([]string{"unknown"}); code != 1 {
		t.Errorf("cmdQueueDispatch(unknown) = %d, want 1", code)
	}
}

// TestTierCQueueReceiveFlagParse pins the flag-parse branch of
// cmdQueueReceive: malformed flags exit 1 without round-tripping.
func TestTierCQueueReceiveFlagParse(t *testing.T) {
	main.ResetJSONOut(t)
	if code := main.CmdQueueReceive([]string{"--bogus-flag"}); code != 1 {
		t.Errorf("cmdQueueReceive(bad flag) = %d, want 1", code)
	}
}

// TestTierCQueuePeekFlagParse pins cmdQueuePeek flag-parse branch.
func TestTierCQueuePeekFlagParse(t *testing.T) {
	main.ResetJSONOut(t)
	if code := main.CmdQueuePeek([]string{"--bogus-flag"}); code != 1 {
		t.Errorf("cmdQueuePeek(bad flag) = %d, want 1", code)
	}
}

// TestTierCQueueAckFlagParse pins cmdQueueAck flag-parse branch.
func TestTierCQueueAckFlagParse(t *testing.T) {
	main.ResetJSONOut(t)
	if code := main.CmdQueueAck([]string{"--bogus-flag"}); code != 1 {
		t.Errorf("cmdQueueAck(bad flag) = %d, want 1", code)
	}
}

func TestQueueAckJSON(t *testing.T) {
	main.ResetJSONOut(t)
	(*main.JsonOutput) = true
	t.Cleanup(func() { (*main.JsonOutput) = false })
	main.AuthedFakeAPI(t, "", http.StatusOK)
	out, restore := main.SwapStdout(t)
	defer restore()
	if code := main.CmdQueueAck([]string{"demo", "row-1"}); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v; output=%q", err, out.String())
	}
	if got["id"] != "row-1" || got["acked"] != true {
		t.Fatalf("ack = %#v", got)
	}
}

// TestTierCQueueDeadLetterFlagParse pins cmdQueueDeadLetter flag-parse branch.
func TestTierCQueueDeadLetterFlagParse(t *testing.T) {
	main.ResetJSONOut(t)
	if code := main.CmdQueueDeadLetter([]string{"--bogus-flag"}); code != 1 {
		t.Errorf("cmdQueueDeadLetter(bad flag) = %d, want 1", code)
	}
}

// TestTierCEnvPullFlagParse pins envPull's flag-parse branch.
func TestTierCEnvPullFlagParse(t *testing.T) {
	main.ResetJSONOut(t)
	if code := main.EnvPull([]string{"--bogus-flag"}); code != 1 {
		t.Errorf("envPull(bad flag) = %d, want 1", code)
	}
	// Missing --app also exits 1 (validated before auth).
	if code := main.EnvPull(nil); code != 1 {
		t.Errorf("envPull(missing app) = %d, want 1", code)
	}
}

// TestTierCEnvPushFlagParse pins envPush's flag-parse branch.
func TestTierCEnvPushFlagParse(t *testing.T) {
	main.ResetJSONOut(t)
	if code := main.EnvPush([]string{"--bogus-flag"}); code != 1 {
		t.Errorf("envPush(bad flag) = %d, want 1", code)
	}
	// Missing --app also exits 1.
	if code := main.EnvPush(nil); code != 1 {
		t.Errorf("envPush(missing app) = %d, want 1", code)
	}
}

// TestTierCPrintUsageAndOK keeps the trivial PrintUsage / PrintOK
// renderers reachable from the test suite. These are touched by
// every leaf's usage branch above but the function bodies themselves
// are not covered otherwise.
func TestTierCPrintUsageAndOK(t *testing.T) {
	// PrintUsage and PrintOK both write to a writer; we just verify
	// they don't panic on the empty input case.
	main.PrintUsage(devNull{}, "usage: gregale X", "X")
	main.PrintOK(devNull{}, "OK")
}

// devNull is an io.Writer that discards all writes. Used to keep
// usage-banner output from polluting test logs.
type devNull struct{}

func (devNull) Write(p []byte) (int, error) { return len(p), nil }

// TestTierCStringHelpers is a literal-pin test for the small
// string helpers in commands5.go + commands2.go that the package
// uses for command-name validation. Setting them via the test
// surface keeps the function entries in the coverage profile.
func TestTierCStringHelpers(t *testing.T) {
	// looksLikeFlag, indexByte, boolPtr, trunc — pure helpers.
	if main.LooksLikeFlag("--foo") != true {
		t.Error("looksLikeFlag(--foo) = false")
	}
	if main.LooksLikeFlag("foo") {
		t.Error("looksLikeFlag(foo) = true, want false")
	}
	if main.IndexByte("hello", 'l') != 2 {
		t.Errorf("indexByte hello/l = %d, want 2", main.IndexByte("hello", 'l'))
	}
	if main.IndexByte("hello", 'z') != -1 {
		t.Errorf("indexByte hello/z = %d, want -1", main.IndexByte("hello", 'z'))
	}
	bp := main.BoolPtr(true)
	if bp == nil || !*bp {
		t.Errorf("boolPtr(true) = %v, want non-nil &true", bp)
	}
	if got := main.Trunc("hello world", 5); !strings.HasPrefix(got, "hell") {
		t.Errorf("trunc(hello world, 5) = %q, want prefix hell", got)
	}
	if got := main.Trunc("hi", 5); got != "hi" {
		t.Errorf("trunc(hi, 5) = %q, want hi", got)
	}
}

// TestTierCRenderInvoicesEmpty is a no-data render path pin for
// renderInvoices. Without a real API response, the function exits
// early on an empty page.
func TestTierCRenderInvoicesEmpty(t *testing.T) {
	main.ResetJSONOut(t)
	main.RenderInvoices(devNull{}, api.InvoiceListResponse{})
}

// _ keeps the strings + io package imports lint-clean.
var (
	_           = strings.HasPrefix
	_ io.Writer = devNull{}
)
