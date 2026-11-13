package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"google.golang.org/grpc"

	secretdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/secretd/v1"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261110210000 slice 3f — drain headers open by reference through
// secretd when FAAS_GATEWAY_LOG_DRAIN_OPENER=secretd.

func TestLogDrainHeaderOpenerFromEnv(t *testing.T) {
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	for _, tc := range []struct {
		name string
		env  map[string]string
		node string
		want string // "" = local opener, no error
	}{
		{name: "unset", node: "fsn-2.faas"},
		{name: "local", env: map[string]string{"FAAS_GATEWAY_LOG_DRAIN_OPENER": "local"}, node: "fsn-2.faas"},
		{name: "unknown", env: map[string]string{"FAAS_GATEWAY_LOG_DRAIN_OPENER": "maybe"}, node: "fsn-2.faas", want: "want local or secretd"},
		{name: "no node", env: map[string]string{"FAAS_GATEWAY_LOG_DRAIN_OPENER": "secretd"}, want: "FAAS_NODE_NAME"},
		{name: "no target", env: map[string]string{"FAAS_GATEWAY_LOG_DRAIN_OPENER": "secretd"}, node: "fsn-2.faas", want: "FAAS_GATEWAY_SECRETD_TARGET"},
		{name: "unix target", env: map[string]string{"FAAS_GATEWAY_LOG_DRAIN_OPENER": "secretd",
			"FAAS_GATEWAY_SECRETD_TARGET": "unix:///run/faas/secretd.sock"}, node: "fsn-2.faas", want: "FAAS_GATEWAY_SECRETD_TARGET"},
		{name: "no TLS", env: map[string]string{"FAAS_GATEWAY_LOG_DRAIN_OPENER": "secretd",
			"FAAS_GATEWAY_SECRETD_TARGET": "tcp://10.0.0.1:8098"}, node: "fsn-2.faas", want: "FAAS_SECRETD_TLS"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opener, closeFn, err := logDrainHeaderOpenerFromEnv(context.Background(), func(k string) string { return tc.env[k] }, tc.node, quiet)
			if tc.want == "" {
				if err != nil || opener != nil {
					t.Fatalf("opener = %v, %v; want local", opener != nil, err)
				}
				closeFn()
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

type fakeSecretdDrains struct {
	secretdpb.SecretdClient
	got    *secretdpb.OpenLogDrainHeaderRequest
	header string
	err    error
}

func (f *fakeSecretdDrains) OpenLogDrainHeader(_ context.Context, req *secretdpb.OpenLogDrainHeaderRequest, _ ...grpc.CallOption) (*secretdpb.OpenLogDrainHeaderResponse, error) {
	f.got = req
	if f.err != nil {
		return nil, f.err
	}
	return &secretdpb.OpenLogDrainHeaderResponse{Header: f.header}, nil
}

func TestSecretdLogDrainHeadersByReference(t *testing.T) {
	rpc := &fakeSecretdDrains{header: "Bearer t"}
	header, err := secretdLogDrainHeaders(rpc)(context.Background(), "app-1", "drain-1")
	if err != nil || header != "Bearer t" || rpc.got.GetAppId() != "app-1" || rpc.got.GetDrainId() != "drain-1" {
		t.Fatalf("header = %q, %v, request %v", header, err, rpc.got)
	}
}

// With a secretd opener, a drain worker asks for its header by reference and
// never needs the edge identity; a refusal stops the worker from starting.
func TestDrainWorkerOpensHeaderThroughSecretd(t *testing.T) {
	m := newAppLogDrainManager(nil, nil, nil, nil, nil)
	refused := errors.New("secretd: PermissionDenied")
	var asked string
	m.openHeader = func(_ context.Context, appID, drainID string) (string, error) {
		asked = appID + "/" + drainID
		return "", refused
	}
	_, err := m.startWorkerLocked(context.Background(), state.AppLogDrain{ID: "drain-1", AppID: "app-1", AuthHeaderSealed: []byte("sealed")})
	if !errors.Is(err, refused) || asked != "app-1/drain-1" {
		t.Fatalf("start = %v, asked %q", err, asked)
	}
}
