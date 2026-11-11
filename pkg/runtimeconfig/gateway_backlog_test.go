package runtimeconfig_test

import (
	"encoding/json"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimeconfig"
)

func TestApplyGatewayAwaitingResponseMax(t *testing.T) {
	cases := []struct {
		name    string
		key     string
		value   string
		handled bool
		wantErr bool
		want    int64
	}{
		{name: "raised", key: runtimeconfig.KeyGatewayAwaitingResponseMax, value: "4096", handled: true, want: 4096},
		{name: "lowest", key: runtimeconfig.KeyGatewayAwaitingResponseMax, value: "64", handled: true, want: 64},
		{name: "below the floor", key: runtimeconfig.KeyGatewayAwaitingResponseMax, value: "10", handled: true, wantErr: true, want: api.GatewayAwaitingResponseMax},
		{name: "above the ceiling", key: runtimeconfig.KeyGatewayAwaitingResponseMax, value: "100000", handled: true, wantErr: true, want: api.GatewayAwaitingResponseMax},
		{name: "not an integer", key: runtimeconfig.KeyGatewayAwaitingResponseMax, value: `"many"`, handled: true, wantErr: true, want: api.GatewayAwaitingResponseMax},
		{name: "other key", key: runtimeconfig.KeyHSTS, value: "true", handled: false, want: api.GatewayAwaitingResponseMax},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var max atomic.Int64
			max.Store(api.GatewayAwaitingResponseMax)
			handled, err := runtimeconfig.ApplyGatewayAwaitingResponseMax(&max, tc.key, json.RawMessage(tc.value))
			if handled != tc.handled || (err != nil) != tc.wantErr {
				t.Fatalf("handled=%v err=%v, want handled=%v err=%v", handled, err, tc.handled, tc.wantErr)
			}
			if got := max.Load(); got != tc.want {
				t.Fatalf("max = %d, want %d", got, tc.want)
			}
		})
	}
}
