package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestSecretAckProgressRequiresEveryReloadEnabledTarget(t *testing.T) {
	secret := api.AppSecretResponse{
		DeliveryVersion: 4,
		RuntimeReloadObservations: []api.SecretRuntimeReloadObservation{
			{InstanceID: "one", ReloadSupport: "enabled", Reported: true, Version: 4, ApplicationAckVersion: 4, ApplicationAck: "applied"},
			{InstanceID: "two", ReloadSupport: "enabled", Reported: true, Version: 4},
		},
	}
	enabled, pending, err := secretAckProgress(secret)
	if err != nil || enabled != 2 || pending != 1 {
		t.Fatalf("progress = enabled %d pending %d err %v, want 2/1/nil", enabled, pending, err)
	}
	secret.RuntimeReloadObservations[1].ApplicationAckVersion = 4
	secret.RuntimeReloadObservations[1].ApplicationAck = "applied"
	enabled, pending, err = secretAckProgress(secret)
	if err != nil || enabled != 2 || pending != 0 {
		t.Fatalf("all applied progress = enabled %d pending %d err %v, want 2/0/nil", enabled, pending, err)
	}
}

func TestSecretAckProgressFailsClosedOnUnknownOrDisabledTargets(t *testing.T) {
	for _, support := range []string{"disabled", "unknown", ""} {
		t.Run(support, func(t *testing.T) {
			secret := api.AppSecretResponse{DeliveryVersion: 1, RuntimeReloadObservations: []api.SecretRuntimeReloadObservation{{
				InstanceID: "legacy", ReloadSupport: support,
			}}}
			if _, _, err := secretAckProgress(secret); err == nil {
				t.Fatalf("support %q unexpectedly accepted", support)
			}
		})
	}
}

func TestSecretAckProgressAcceptsCurrentApplicationProofForLegacyTarget(t *testing.T) {
	secret := api.AppSecretResponse{DeliveryVersion: 2, RuntimeReloadObservations: []api.SecretRuntimeReloadObservation{{
		InstanceID: "legacy", ReloadSupport: "unknown", ApplicationAckVersion: 2, ApplicationAck: "applied",
	}}}
	if enabled, pending, err := secretAckProgress(secret); err != nil || enabled != 1 || pending != 0 {
		t.Fatalf("legacy proof progress = enabled %d pending %d err %v, want 1/0/nil", enabled, pending, err)
	}
}

func TestSecretAckProgressWaitsForUnsupportedTargetAfterRestart(t *testing.T) {
	secret := api.AppSecretResponse{DeliveryVersion: 2, RuntimeReloadObservations: []api.SecretRuntimeReloadObservation{{
		InstanceID: "legacy", ReloadSupport: "disabled",
	}}}
	if targets, pending, err := secretAckProgressWithRestart(secret, "fresh-instance"); err != nil || targets != 1 || pending != 1 {
		t.Fatalf("unacknowledged restart progress = targets %d pending %d err %v, want 1/1/nil", targets, pending, err)
	}
	secret.RuntimeReloadObservations[0].ApplicationAckVersion = 2
	secret.RuntimeReloadObservations[0].ApplicationAck = "applied"
	if targets, pending, err := secretAckProgressWithRestart(secret, "fresh-instance"); err != nil || targets != 1 || pending != 0 {
		t.Fatalf("acknowledged restart progress = targets %d pending %d err %v, want 1/0/nil", targets, pending, err)
	}
}

func TestSecretAckProgressDefersPriorRuntimeFailureAfterRestart(t *testing.T) {
	secret := api.AppSecretResponse{DeliveryVersion: 2, RuntimeReloadObservations: []api.SecretRuntimeReloadObservation{
		{InstanceID: "old-instance", ReloadSupport: "disabled", ApplicationAckVersion: 2, ApplicationAck: "failed"},
		{InstanceID: "fresh-instance", ReloadSupport: "disabled", ApplicationAckVersion: 2, ApplicationAck: "applied"},
	}}
	if targets, pending, err := secretAckProgressWithRestart(secret, "fresh-instance"); err != nil || targets != 2 || pending != 1 {
		t.Fatalf("restart progress = targets %d pending %d err %v, want 2/1/nil", targets, pending, err)
	}
	secret.RuntimeReloadObservations[1].ApplicationAck = "failed"
	if _, _, err := secretAckProgressWithRestart(secret, "fresh-instance"); err == nil {
		t.Fatal("fresh runtime application failure unexpectedly accepted")
	}
}

func TestSecretAckProgressSurfacesApplicationFailure(t *testing.T) {
	secret := api.AppSecretResponse{DeliveryVersion: 2, RuntimeReloadObservations: []api.SecretRuntimeReloadObservation{{
		InstanceID: "one", ReloadSupport: "enabled", Reported: true, Version: 2,
		ApplicationAckVersion: 2, ApplicationAck: "failed",
	}}}
	if _, _, err := secretAckProgress(secret); err == nil {
		t.Fatal("failed application acknowledgement unexpectedly accepted")
	}
}

func TestFindSecretStatusMatchesScope(t *testing.T) {
	list := api.AppSecretListResponse{Secrets: []api.AppSecretResponse{
		{Key: "DATABASE_URL", Scope: "staging"},
		{Key: "DATABASE_URL", Scope: "prod"},
	}}
	got, ok := findSecretStatus(list, "DATABASE_URL", "prod")
	if !ok || got.Scope != "prod" {
		t.Fatalf("findSecretStatus = %+v, %v; want prod row", got, ok)
	}
}

func TestWaitForSecretApplicationAckRejectsIncompleteRoster(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSONTest(w, api.AppSecretListResponse{Secrets: []api.AppSecretResponse{{
			Key: "DATABASE_URL", Scope: "prod", DeliveryVersion: 3,
		}}})
	}))
	defer server.Close()
	client := api.NewClient(server.URL, "test-token")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := waitForSecretApplicationAck(ctx, client, "app", "DATABASE_URL", "prod"); err == nil {
		t.Fatal("incomplete runtime roster unexpectedly accepted")
	}
}

func TestWaitForSecretApplicationAckSucceedsForEmptyCompleteRoster(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSONTest(w, api.AppSecretListResponse{Secrets: []api.AppSecretResponse{{
			Key: "DATABASE_URL", Scope: "prod", DeliveryVersion: 3, RuntimeReloadTargetsComplete: true,
		}}})
	}))
	defer server.Close()
	client := api.NewClient(server.URL, "test-token")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if count, err := waitForSecretApplicationAck(ctx, client, "app", "DATABASE_URL", "prod"); err != nil || count != 0 {
		t.Fatalf("wait returned count %d err %v, want 0/nil", count, err)
	}
}

func TestWaitForSecretApplicationAckAfterRestartRejectsEmptyRoster(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSONTest(w, api.AppSecretListResponse{Secrets: []api.AppSecretResponse{{
			Key: "DATABASE_URL", Scope: "prod", DeliveryVersion: 3, RuntimeReloadTargetsComplete: true,
		}}})
	}))
	defer server.Close()
	client := api.NewClient(server.URL, "test-token")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := waitForSecretApplicationAckAfterRestart(ctx, client, "app", "DATABASE_URL", "prod", "instance-1"); err == nil {
		t.Fatal("restart with no authorized runtime unexpectedly succeeded")
	}
}

func TestWaitForSecretApplicationAckTimesOutOnMissingAck(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSONTest(w, api.AppSecretListResponse{Secrets: []api.AppSecretResponse{{
			Key: "DATABASE_URL", Scope: "prod", DeliveryVersion: 3, RuntimeReloadTargetsComplete: true,
			RuntimeReloadObservations: []api.SecretRuntimeReloadObservation{{
				InstanceID: "instance-1", ReloadSupport: "enabled", Reported: true, Version: 3,
			}},
		}}})
	}))
	defer server.Close()
	client := api.NewClient(server.URL, "test-token")
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	if _, err := waitForSecretApplicationAck(ctx, client, "app", "DATABASE_URL", "prod"); err == nil {
		t.Fatal("wait without acknowledgement unexpectedly succeeded")
	}
}
