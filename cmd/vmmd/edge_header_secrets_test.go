package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	secretdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/secretd/v1"
)

// adr: 20261110210000 — slice 3i: vmmd opens edge-rule header values only
// when told to, through secretd with a complete mTLS configuration.
func TestEdgeHeaderSecretOpenerFromEnv(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	appOf := func(string) (string, error) { return "app-a", nil }
	for _, tc := range []struct {
		name string
		env  map[string]string
		want string // "" = off (nil opener, no error)
	}{
		{name: "unset is off"},
		{name: "off", env: map[string]string{"FAAS_VMMD_EDGE_HEADER_SECRETS": "off"}},
		{name: "unknown", env: map[string]string{"FAAS_VMMD_EDGE_HEADER_SECRETS": "local"}, want: "want off or secretd"},
		{name: "secretd without target", env: map[string]string{"FAAS_VMMD_EDGE_HEADER_SECRETS": "secretd"}, want: "FAAS_VMMD_EDGE_HEADER_SECRETS=secretd needs FAAS_SECRETD_TARGET"},
		{name: "secretd on a unix socket", env: map[string]string{"FAAS_VMMD_EDGE_HEADER_SECRETS": "secretd", "FAAS_SECRETD_TARGET": "unix:///run/faas/secretd.sock"}, want: "want a tcp://"},
		{name: "secretd without TLS", env: map[string]string{"FAAS_VMMD_EDGE_HEADER_SECRETS": "secretd", "FAAS_SECRETD_TARGET": "tcp://10.0.0.1:8098"}, want: "TLS"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opener, closeFn, err := edgeHeaderSecretOpenerFromEnv(context.Background(), func(k string) string { return tc.env[k] }, appOf, log)
			if tc.want == "" {
				if err != nil || opener != nil {
					t.Fatalf("= %v, %v; want off", opener, err)
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

type fakeSecretdClient struct {
	secretdpb.SecretdClient
	asked []string
	fail  bool
}

func (f *fakeSecretdClient) OpenEdgeHeaderSecret(_ context.Context, req *secretdpb.OpenEdgeHeaderSecretRequest, _ ...grpc.CallOption) (*secretdpb.OpenEdgeHeaderSecretResponse, error) {
	f.asked = append(f.asked, req.GetTargetAppId()+"/"+req.GetSecretAppId()+"/"+req.GetSecretId())
	if f.fail {
		return nil, status.Error(codes.PermissionDenied, "not the owner")
	}
	return &secretdpb.OpenEdgeHeaderSecretResponse{Value: "v-" + req.GetSecretId()}, nil
}

// The target app comes from the live instance, values are reused for a
// while per target app, and a refusal is never cached.
func TestEdgeHeaderSecretOpenerAsksForTheInstancesApp(t *testing.T) {
	rpc := &fakeSecretdClient{}
	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	apps := map[string]string{"i-1": "app-a", "i-2": "app-b"}
	o := newEdgeHeaderSecretOpener(rpc, func(instance string) (string, error) {
		app, ok := apps[instance]
		if !ok {
			return "", errors.New("not live")
		}
		return app, nil
	}, func() time.Time { return now })
	ctx := context.Background()
	for range 2 {
		if v, err := o.OpenEdgeHeaderSecret(ctx, "i-1", "app-a", "s-1"); err != nil || v != "v-s-1" {
			t.Fatalf("open = %q, %v", v, err)
		}
	}
	if len(rpc.asked) != 1 || rpc.asked[0] != "app-a/app-a/s-1" {
		t.Fatalf("asked = %v, want one ask for app-a", rpc.asked)
	}
	// Another instance's app is its own key.
	if _, err := o.OpenEdgeHeaderSecret(ctx, "i-2", "app-a", "s-1"); err != nil || rpc.asked[1] != "app-b/app-a/s-1" {
		t.Fatalf("other instance = %v, %v", rpc.asked, err)
	}
	if _, err := o.OpenEdgeHeaderSecret(ctx, "i-9", "app-a", "s-1"); err == nil {
		t.Fatal("an instance that is not live opened a value")
	}
	now = now.Add(edgeHeaderSecretTTL)
	rpc.fail = true
	if _, err := o.OpenEdgeHeaderSecret(ctx, "i-1", "app-a", "s-1"); status.Code(errors.Unwrap(err)) != codes.PermissionDenied {
		t.Fatalf("after the TTL a refusal = %v", err)
	}
	if _, err := o.OpenEdgeHeaderSecret(ctx, "i-1", "app-a", "s-1"); err == nil {
		t.Fatal("a refusal was cached as a value")
	}
}
