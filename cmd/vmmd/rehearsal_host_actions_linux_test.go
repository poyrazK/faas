//go:build linux

// adr: 20261017201005
package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/workloadidentity"
)

type rehearsalHostPolicy struct{}

func (rehearsalHostPolicy) ScenarioRehearsalNetworkRun(context.Context, string) (string, error) {
	return strings.Repeat("a", 32), nil
}

func TestRehearsalNetworkHostBrokersRefuseBeforeExternalActions(t *testing.T) {
	mgr := fcvm.NewManager(nil, nil, fcvm.Paths{}, "test", nil, nil).
		RegisterInstanceForTest("instance", "deployment", "app", "account").WithRehearsalNetworkPolicies(rehearsalHostPolicy{})
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := workloadidentity.NewSigner(key, workloadidentity.DefaultIssuer, "test", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	t.Run("relay", func(t *testing.T) {
		dialed := false
		r := &OutboundRelayReceiver{ctx: t.Context(), log: log, mgr: mgr, signer: signer, now: time.Now, dial: func(context.Context) (net.Conn, error) { dialed = true; return nil, errors.New("must not dial") }}
		server, client := net.Pipe()
		defer server.Close()
		defer client.Close()
		if _, err := r.handleGuestStream("instance", server); !errors.Is(err, fcvm.ErrRehearsalExternalActionDenied) || dialed {
			t.Fatalf("relay escaped: dialed=%v err=%v", dialed, err)
		}
	})
	t.Run("identity", func(t *testing.T) {
		r := &WorkloadIdentityReceiver{ctx: t.Context(), log: log, mgr: mgr, signer: signer, now: time.Now}
		server, client := net.Pipe()
		defer server.Close()
		defer client.Close()
		done := make(chan error, 1)
		go func() { _, err := r.handleGuestStream("instance", server); done <- err }()
		body := mustJSON(t, workloadIdentityRequest{Audience: "https://api.example.test"})
		frame := make([]byte, 4+len(body))
		binary.BigEndian.PutUint32(frame, uint32(len(body)))
		copy(frame[4:], body)
		if _, err := client.Write(frame); err != nil {
			t.Fatal(err)
		}
		responseBody, err := readIdentityFrame(client)
		if err != nil {
			t.Fatal(err)
		}
		var response workloadIdentityResponse
		if err := json.Unmarshal(responseBody, &response); err != nil {
			t.Fatal(err)
		}
		if response.AccessToken != "" || response.Error != "rehearsal_identity_unavailable" {
			t.Fatalf("minted external authority: %+v", response)
		}
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	})
	t.Run("event fanout", func(t *testing.T) {
		published := false
		r := &FrameworkReadyReceiver{ctx: t.Context(), log: log, mgr: mgr, eventPublisher: func(context.Context, string, []byte) error { published = true; return nil }}
		r.dispatchEventPublish("instance", []byte(`{"id":"event"}`))
		if published {
			t.Fatal("contained guest reached account event fanout")
		}
	})
	t.Run("guest cannot mint simulation relay capability", func(t *testing.T) {
		ordinary := fcvm.NewManager(nil, nil, fcvm.Paths{}, "test", nil, nil).RegisterInstanceForTest("ordinary", "deployment", "app", "account")
		r := &WorkloadIdentityReceiver{ctx: t.Context(), log: log, mgr: ordinary, signer: signer, now: time.Now}
		server, client := net.Pipe()
		defer server.Close()
		defer client.Close()
		done := make(chan error, 1)
		go func() { _, err := r.handleGuestStream("ordinary", server); done <- err }()
		body := mustJSON(t, workloadIdentityRequest{Audience: workloadidentity.RehearsalRelayAudiencePrefix + strings.Repeat("a", 32)})
		frame := make([]byte, 4+len(body))
		binary.BigEndian.PutUint32(frame, uint32(len(body)))
		copy(frame[4:], body)
		if _, err := client.Write(frame); err != nil {
			t.Fatal(err)
		}
		response, err := readIdentityFrame(client)
		if err != nil {
			t.Fatal(err)
		}
		var value workloadIdentityResponse
		if err := json.Unmarshal(response, &value); err != nil || value.AccessToken != "" || value.Error != "invalid_audience" {
			t.Fatalf("guest minted host-only authority: %+v %v", value, err)
		}
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	})
}
