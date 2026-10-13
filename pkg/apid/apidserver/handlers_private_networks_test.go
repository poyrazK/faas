package apidserver_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 009
func TestPrivateNetworkFabricLifecycle(t *testing.T) {
	t.Setenv("FAAS_PRIVATE_NETWORK_FABRIC_ENABLED", "true")
	e := apidserver.Setup(t, api.PlanScale)

	rec := e.Do(t, "POST", "/v1/networks", api.CreatePrivateNetworkRequest{
		Name: "prod", Region: "fra1", CIDR: "10.42.1.5/28", AllowedCIDRs: []string{"10.42.1.0/30"},
	}, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var created api.PrivateNetwork
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode POST: %v", err)
	}
	if created.ID == "" || created.CIDR != "10.42.1.0/28" || created.Status != api.PrivateNetworkStatusReady || len(created.AllowedCIDRs) != 1 {
		t.Fatalf("created network = %+v", created)
	}
	rec = e.Do(t, "PUT", "/v1/networks/"+created.ID+"/policy", api.UpdatePrivateNetworkPolicyRequest{AllowedCIDRs: []string{"10.42.1.4/30"}}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("policy update status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var updated api.PrivateNetwork
	if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil || len(updated.AllowedCIDRs) != 1 || updated.AllowedCIDRs[0] != "10.42.1.4/30" {
		t.Fatalf("updated network = %+v, err=%v", updated, err)
	}

	rec = e.Do(t, "GET", "/v1/networks", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET list status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var list api.PrivateNetworkListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list.Networks) != 1 || list.Networks[0].ID != created.ID {
		t.Fatalf("network list = %+v", list.Networks)
	}

	rec = e.Do(t, "GET", "/v1/networks/"+created.ID, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	apidserver.MustSeedApp(t, e, "network-attached")
	t.Setenv("FAAS_PRIVATE_NETWORK_ENABLED", "true")
	rec = e.Do(t, "PUT", "/v1/apps/network-attached/network/private", api.AppPrivateNetworkAttachmentRequest{NetworkID: created.ID}, nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("attachment status = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	var attachmentResp api.AppPrivateNetworkAttachmentResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &attachmentResp); err != nil {
		t.Fatalf("decode attachment response: %v", err)
	}
	if attachmentResp.Attachment == nil || attachmentResp.Attachment.Address != "10.42.1.2" {
		t.Fatalf("attachment address = %+v, want stable member 10.42.1.2", attachmentResp.Attachment)
	}
	rec = e.Do(t, "GET", "/v1/networks/"+created.ID+"/members", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("members status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var members api.PrivateNetworkMembersResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &members); err != nil {
		t.Fatalf("decode members: %v", err)
	}
	if members.NetworkID != created.ID || members.CIDR != "10.42.1.0/28" || members.Capacity != 13 || members.Used != 1 || members.Available != 12 || len(members.Members) != 1 {
		t.Fatalf("members = %+v", members)
	}
	if member := members.Members[0]; member.OwnerType != "app" || member.OwnerID == "" || member.Address != "10.42.1.2" {
		t.Fatalf("member = %+v", member)
	}
	if err := (*e.Store()).DeletePrivateNetwork(t.Context(), (*e.Acct()).ID, created.ID); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("delete attached network err = %v, want conflict", err)
	}

	rec = e.Do(t, "DELETE", "/v1/apps/network-attached/network/private", nil, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("detach status = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	rec = e.Do(t, "DELETE", "/v1/networks/"+created.ID, nil, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE status = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
}

func TestPrivateNetworkDeleteEmitsFabricTeardownNotification(t *testing.T) {
	t.Setenv("FAAS_PRIVATE_NETWORK_FABRIC_ENABLED", "true")
	e := apidserver.Setup(t, api.PlanScale)
	notifier := &apidserver.CaptureNotifier{}
	(*(*e.S()).Notif()) = notifier

	rec := e.Do(t, "POST", "/v1/networks", api.CreatePrivateNetworkRequest{
		Name: "cleanup", Region: "fra1", CIDR: "10.43.0.0/24",
	}, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var created api.PrivateNetwork
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode POST: %v", err)
	}

	rec = e.Do(t, "DELETE", "/v1/networks/"+created.ID, nil, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE status = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	notifications := notifier.ByChannel(db.NotifyPrivateNetworkChanged)
	if len(notifications) != 1 {
		t.Fatalf("private network notifications = %d, want 1", len(notifications))
	}
	var payload struct {
		Kind      string `json:"kind"`
		AccountID string `json:"account_id"`
		NetworkID string `json:"network_id"`
		Region    string `json:"region"`
		CIDR      string `json:"cidr"`
	}
	if err := json.Unmarshal([]byte((*notifications[0].Payload())), &payload); err != nil {
		t.Fatalf("decode teardown notification: %v", err)
	}
	if payload.Kind != "private_network_deleted" || payload.AccountID != (*e.Acct()).ID || payload.NetworkID != created.ID || payload.Region != "fra1" || payload.CIDR != "10.43.0.0/24" {
		t.Fatalf("teardown payload = %+v", payload)
	}
}

func TestPrivateNetworkFabricFlagOff(t *testing.T) {
	t.Setenv("FAAS_PRIVATE_NETWORK_FABRIC_ENABLED", "false")
	e := apidserver.Setup(t, api.PlanScale)
	for _, method := range []string{"GET", "POST"} {
		var body any
		if method == "POST" {
			body = api.CreatePrivateNetworkRequest{Name: "prod", Region: "fra1", CIDR: "10.42.0.0/24"}
		}
		rec := e.Do(t, method, "/v1/networks", body, nil)
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s status = %d, want 503; body=%s", method, rec.Code, rec.Body.String())
		}
	}
	rec := e.Do(t, "GET", "/v1/networks/net-missing/members", nil, nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("members status = %d, want 503; body=%s", rec.Code, rec.Body.String())
	}
}
