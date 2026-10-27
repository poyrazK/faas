package sched_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/onebox-faas/faas/pkg/sched"
)

// production-us hunt #14 (H14-82): a cold boot failed with vmmd's "readiness:
// context canceled" and schedd logged nothing, so the canceller was unknown.
// The failure line must say whether schedd's own boot context had ended and
// which gRPC code came back.
func TestLogVMMBootFailureNamesTheCanceller(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	cases := []struct {
		name        string
		ctx         context.Context
		err         error
		wantCode    string
		wantCtxErr  string
		wantCausErr string
	}{
		{name: "vmmd stopped the boot", ctx: context.Background(),
			err:      status.Error(codes.Unknown, "wake i: cold boot: vmm: readiness: context canceled"),
			wantCode: "Unknown"},
		{name: "schedd cancelled", ctx: cancelled, err: status.Error(codes.Canceled, "context canceled"),
			wantCode: "Canceled", wantCtxErr: "context canceled", wantCausErr: "context canceled"},
		{name: "connection closed", ctx: context.Background(),
			err:      status.Error(codes.Canceled, "grpc: the client connection is closing"),
			wantCode: "Canceled"},
		{name: "plain error", ctx: context.Background(), err: errors.New("dial failed"), wantCode: "Unknown"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			log := slog.New(slog.NewJSONHandler(&buf, nil))
			sched.LogVMMBootFailure(log, tc.ctx, "app-1", "ins-1", "wake-1", "node-1", "cold_boot", 340*time.Millisecond, tc.err)
			var line map[string]any
			if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
				t.Fatalf("log line %q: %v", buf.String(), err)
			}
			want := map[string]any{
				"msg": "wake: vmmd boot failed", "level": "WARN", "instance": "ins-1", "wake_id": "wake-1",
				"node": "node-1", "method": "cold_boot", "rpc_ms": float64(340), "grpc_code": tc.wantCode,
				"boot_ctx_err": tc.wantCtxErr, "boot_ctx_cause": tc.wantCausErr, "err": tc.err.Error(),
			}
			for k, v := range want {
				if line[k] != v {
					t.Errorf("%s = %#v, want %#v (line %s)", k, line[k], v, buf.String())
				}
			}
		})
	}
	var buf bytes.Buffer
	sched.LogVMMBootFailure(slog.New(slog.NewJSONHandler(&buf, nil)), context.Background(), "a", "i", "w", "n", "cold_boot", 0, nil)
	if buf.Len() != 0 {
		t.Fatalf("nil error logged %q", buf.String())
	}
}
