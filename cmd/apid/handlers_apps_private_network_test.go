package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func withPrivateNetworkEnabled(t *testing.T, enabled bool) {
	t.Helper()
	if enabled {
		t.Setenv("FAAS_PRIVATE_NETWORK_ENABLED", "true")
	} else {
		t.Setenv("FAAS_PRIVATE_NETWORK_ENABLED", "false")
	}
}

func privateNetworkRequest(networkID, region string, cidrs []string) api.AppPrivateNetworkAttachmentRequest {
	return api.AppPrivateNetworkAttachmentRequest{NetworkID: networkID, Region: region, CIDRs: cidrs}
}

func TestPrivateNetwork_FlagOffBlocksAllHandlers(t *testing.T) {
	withPrivateNetworkEnabled(t, false)
	e := setup(t, api.PlanScale)
	mustSeedApp(t, e, "private-off")
	for _, verb := range []string{"GET", "PUT", "DELETE"} {
		var body any
		if verb == "PUT" {
			body = privateNetworkRequest("prod-vpc", "fra1", []string{"10.20.0.0/16"})
		}
		rec := e.do(t, verb, "/v1/apps/private-off/network/private", body, nil)
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s status = %d, want 503; body=%s", verb, rec.Code, rec.Body.String())
		}
	}
}

func TestPrivateNetwork_PutGetDeleteLifecycle(t *testing.T) {
	withPrivateNetworkEnabled(t, true)
	e := setup(t, api.PlanScale)
	appID := mustSeedApp(t, e, "private-life")

	rec := e.do(t, "PUT", "/v1/apps/private-life/network/private",
		privateNetworkRequest("prod-vpc", "fra1", []string{"10.20.1.1/16", "10.30.0.0/16"}), nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("PUT status = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	var resp api.AppPrivateNetworkAttachmentResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode PUT: %v", err)
	}
	if resp.Attachment == nil || resp.Attachment.Status != api.PrivateNetworkAttachmentStatusPending {
		t.Fatalf("PUT attachment = %+v, want pending", resp.Attachment)
	}
	if got := strings.Join(resp.Attachment.CIDRs, ","); got != "10.20.0.0/16,10.30.0.0/16" {
		t.Fatalf("canonical CIDRs = %q", got)
	}

	rec = e.do(t, "GET", "/v1/apps/private-life/network/private", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode GET: %v", err)
	}
	if resp.Attachment == nil || resp.Attachment.NetworkID != "prod-vpc" || resp.MaxCIDRs != 64 {
		t.Fatalf("GET response = %+v", resp)
	}
	healthStore := state.PrivateNetworkAttachmentHealthStore(e.store)
	attachment, err := e.store.GetAppPrivateNetworkAttachment(t.Context(), e.acct.ID, appID)
	if err != nil {
		t.Fatalf("read attachment for health seed: %v", err)
	}
	if err := healthStore.UpsertPrivateNetworkAttachmentNodeStatus(t.Context(), state.PrivateNetworkAttachmentNodeStatus{
		AccountID: e.acct.ID, AppID: appID, NetworkID: attachment.NetworkID, NodeID: "node-a",
		FabricStatus: "ready", FabricDetail: "bridge ready", RouteStatus: "error", RouteDetail: "route update failed",
	}); err != nil {
		t.Fatalf("seed node health: %v", err)
	}
	rec = e.do(t, "GET", "/v1/apps/private-life/network/private", nil, nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode GET health: %v", err)
	}
	if len(resp.Attachment.Nodes) != 1 || resp.Attachment.Nodes[0].NodeID != "node-a" || resp.Attachment.Nodes[0].RouteStatus != "error" {
		t.Fatalf("GET node health = %#v", resp.Attachment.Nodes)
	}

	rec = e.do(t, "DELETE", "/v1/apps/private-life/network/private", nil, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE status = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	rec = e.do(t, "DELETE", "/v1/apps/private-life/network/private", nil, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("idempotent DELETE status = %d, want 204", rec.Code)
	}
}

func TestPrivateNetwork_PlanAndCIDRGates(t *testing.T) {
	withPrivateNetworkEnabled(t, true)
	e := setup(t, api.PlanFree)
	mustSeedApp(t, e, "private-free")
	rec := e.do(t, "PUT", "/v1/apps/private-free/network/private",
		privateNetworkRequest("prod-vpc", "fra1", []string{"10.20.0.0/16"}), nil)
	if rec.Code != http.StatusPaymentRequired || !strings.Contains(rec.Body.String(), api.CodePlanPrivateNetworkNotAllowed) {
		t.Fatalf("Free PUT status/body = %d/%s", rec.Code, rec.Body.String())
	}

	e = setup(t, api.PlanScale)
	mustSeedApp(t, e, "private-bad")
	for _, req := range []api.AppPrivateNetworkAttachmentRequest{
		privateNetworkRequest("Bad_VPC", "fra1", []string{"10.20.0.0/16"}),
		privateNetworkRequest("prod-vpc", "fra1", []string{"10.0.0.0/8"}),
		privateNetworkRequest("prod-vpc", "fra1", []string{"172.16.0.0/12", "172.16.0.0/16"}),
	} {
		rec := e.do(t, "PUT", "/v1/apps/private-bad/network/private", req, nil)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), api.CodePrivateNetworkInvalid) {
			t.Errorf("bad request status/body = %d/%s", rec.Code, rec.Body.String())
		}
	}
}
