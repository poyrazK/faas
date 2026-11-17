package main

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/pki"
	"github.com/onebox-faas/faas/pkg/rootfs"
)

type localSigner struct{}

func (localSigner) Sign(context.Context, string, string) error { return nil }

func TestArtifactSignerFromEnv(t *testing.T) {
	if signdServerName != pki.SigndServerName {
		t.Fatalf("imaged dials signd as %q; its leaf names %q", signdServerName, pki.SigndServerName)
	}
	var local rootfs.Signer = localSigner{}
	for _, tc := range []struct {
		name string
		env  map[string]string
		want string // error substring; "" keeps the local signer
	}{
		{name: "unset", env: map[string]string{}},
		{name: "local", env: map[string]string{"FAAS_ARTIFACT_SIGNER": "local"}},
		{name: "unknown mode", env: map[string]string{"FAAS_ARTIFACT_SIGNER": "remote"}, want: "want local or signd"},
		{name: "no target", env: map[string]string{"FAAS_ARTIFACT_SIGNER": "signd"}, want: "needs FAAS_SIGND_TARGET"},
		{name: "unix target", env: map[string]string{"FAAS_ARTIFACT_SIGNER": "signd", "FAAS_SIGND_TARGET": "unix:///run/faas/signd.sock"}, want: "want a tcp://"},
		{name: "no TLS", env: map[string]string{"FAAS_ARTIFACT_SIGNER": "signd", "FAAS_SIGND_TARGET": "tcp://10.0.0.1:7443"}, want: "TLS"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, closeFn, err := artifactSignerFromEnv(context.Background(), func(k string) string { return tc.env[k] }, local, nil, slog.Default())
			if tc.want != "" {
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("err = %v, want %q", err, tc.want)
				}
				return
			}
			if err != nil || got != local {
				t.Fatalf("got %T, %v; want the local signer", got, err)
			}
			closeFn()
		})
	}
}
