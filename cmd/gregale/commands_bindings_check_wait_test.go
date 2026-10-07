package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/bindingcheck"
)

type bindingCheckWaitClient struct {
	read func(context.Context, string, string, string) (api.AppBindingInventory, error)
}

func (c bindingCheckWaitClient) GetAppBindingInventoryForDeployment(ctx context.Context, app, scope, deployment string) (api.AppBindingInventory, error) {
	return c.read(ctx, app, scope, deployment)
}

func pendingBindingCheckInventory() api.AppBindingInventory {
	inventory := bindingCheckCLIInventory()
	inventory.Bindings[0].VerificationStatus = "unknown"
	inventory.Bindings[0].Verification.Result = "unknown"
	inventory.Bindings[0].Verification.Reason = "probe_pending"
	return inventory
}

func TestCmdBindingsCheckWaitPinsSelectionAndPrintsOneJSONReport(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodGet || r.URL.Path != "/v1/apps/api/bindings" {
			t.Errorf("wait mutated bindings: %s %s", r.Method, r.URL)
		}
		inventory := pendingBindingCheckInventory()
		if calls > 1 {
			if r.URL.Query().Get("deployment_id") != "deployment-1" || r.URL.Query().Get("scope") != "production" {
				t.Errorf("selection drifted: %s", r.URL)
			}
			inventory = bindingCheckCLIInventory()
			inventory.RequestedDeploymentID = "deployment-1"
			inventory.Scope = "production"
		}
		_ = json.NewEncoder(w).Encode(inventory)
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	oldOut, oldErr, oldJSON := osStdout, osStderr, jsonOutput
	var out, errs bytes.Buffer
	osStdout, osStderr, jsonOutput = &out, &errs, false
	defer func() { osStdout, osStderr, jsonOutput = oldOut, oldErr, oldJSON }()
	code := run([]string{"bindings", "check", "api", "--wait", "--timeout", "1s", "--poll-interval", "1ms", "--json"})
	report := decodeBindingCheckCLI(t, out.String())
	if code != 0 || !report.Passed || report.ExpectedDeploymentID != "deployment-1" || calls != 2 || errs.Len() != 0 {
		t.Fatalf("exit=%d calls=%d report=%+v stderr=%s", code, calls, report, errs.String())
	}
}

func TestBindingCheckWaitStopsOnTimeoutCancelAndTerminalBlockers(t *testing.T) {
	for _, test := range []struct {
		name      string
		inventory api.AppBindingInventory
		cancel    bool
		want      error
		calls     int
	}{
		{"timeout", pendingBindingCheckInventory(), false, context.DeadlineExceeded, 1},
		{"cancel", pendingBindingCheckInventory(), true, context.Canceled, 1},
		{"missing evidence", func() api.AppBindingInventory {
			i := pendingBindingCheckInventory()
			i.Bindings[0].Verification = nil
			return i
		}(), false, nil, 1},
		{"failed probe", func() api.AppBindingInventory {
			i := pendingBindingCheckInventory()
			i.Bindings[0].VerificationStatus = "failed"
			return i
		}(), false, nil, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
			defer cancel()
			calls := 0
			client := bindingCheckWaitClient{read: func(context.Context, string, string, string) (api.AppBindingInventory, error) {
				calls++
				if test.cancel {
					cancel()
				}
				return test.inventory, nil
			}}
			report, err := pollBindingCheck(ctx, client, bindingcheck.Policy{App: "api", MaxVerificationAge: time.Minute * 10}, true, time.Second)
			if !errors.Is(err, test.want) || report.Passed || report.App != "api" || calls != test.calls {
				t.Fatalf("report=%+v err=%v calls=%d", report, err, calls)
			}
		})
	}
}

func TestBindingCheckWaitRejectsChangedDeployment(t *testing.T) {
	calls := 0
	client := bindingCheckWaitClient{read: func(_ context.Context, _ string, scope, deployment string) (api.AppBindingInventory, error) {
		calls++
		if calls == 1 {
			return pendingBindingCheckInventory(), nil
		}
		if scope != "production" || deployment != "deployment-1" {
			t.Fatalf("unpinned read: %s %s", scope, deployment)
		}
		i := bindingCheckCLIInventory()
		i.Scope = scope
		i.RequestedDeploymentID = deployment
		i.VerificationDeploymentID = "deployment-2"
		return i, nil
	}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	report, err := pollBindingCheck(ctx, client, bindingcheck.Policy{App: "api", MaxVerificationAge: time.Minute * 10}, true, time.Millisecond)
	if err != nil || report.Passed || calls != 2 {
		t.Fatalf("report=%+v err=%v calls=%d", report, err, calls)
	}
}

func TestBindingCheckWaitRecognizesPendingRefreshAndApplicationAck(t *testing.T) {
	now := time.Now().UTC()
	inventory := bindingCheckCLIInventory()
	adoption := &api.BindingApplicationAdoption{Source: "application_ack", Complete: true, ObservedAt: now, SecretsExpected: 6, SecretsObserved: 6}
	for _, key := range api.BindingCredentialSecretKeys(api.BindingTypeObjectStorage, "ASSETS") {
		adoption.Targets = append(adoption.Targets, api.BindingApplicationAckTarget{DeploymentID: "deployment-1", InstanceID: "runtime", RuntimeState: "running", Key: key, CurrentVersion: 1, ReloadSupport: "enabled", ReloadVersion: 1, Projection: "updated", Signal: "sent", ReloadAt: &now, ProcessGeneration: strings.Repeat("a", 32)})
	}
	inventory.Bindings[0].ApplicationAdoption = adoption
	policy := bindingcheck.Policy{App: "api", MaxVerificationAge: time.Minute * 10, RequireApplicationAck: true}
	report, err := bindingcheck.Evaluate(inventory, policy, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if report.Passed || !bindingCheckCanProgress(report, inventory) {
		t.Fatalf("missing ACK not waitable: %+v", report)
	}
	adoption.Targets[0].ReloadSupport = "disabled"
	report, err = bindingcheck.Evaluate(inventory, policy, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if bindingCheckCanProgress(report, inventory) {
		t.Fatal("disabled reload should require action")
	}
	inventory = bindingCheckCLIInventory()
	pending := true
	inventory.Bindings[0].RotationPending = &pending
	inventory.Bindings[0].Refresh = &api.BindingRefresh{Status: "retrying"}
	report, err = bindingcheck.Evaluate(inventory, bindingcheck.Policy{App: "api", MaxVerificationAge: time.Minute * 10}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if report.Passed || !bindingCheckCanProgress(report, inventory) {
		t.Fatalf("pending refresh not waitable: %+v", report)
	}
	inventory.Bindings[0].Refresh.Status = "failed"
	report, err = bindingcheck.Evaluate(inventory, bindingcheck.Policy{App: "api", MaxVerificationAge: time.Minute * 10}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if bindingCheckCanProgress(report, inventory) {
		t.Fatal("failed refresh should require action")
	}
}

func TestCmdBindingsCheckWaitTimeoutPrintsLastBlockedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("wait mutated bindings: %s", r.Method)
		}
		_ = json.NewEncoder(w).Encode(pendingBindingCheckInventory())
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	oldOut, oldErr, oldJSON := osStdout, osStderr, jsonOutput
	var out, errs bytes.Buffer
	osStdout, osStderr, jsonOutput = &out, &errs, false
	defer func() { osStdout, osStderr, jsonOutput = oldOut, oldErr, oldJSON }()
	code := run([]string{"bindings", "check", "api", "--wait", "--timeout", "50ms", "--poll-interval", "1s", "--json"})
	report := decodeBindingCheckCLI(t, out.String())
	if code != 1 || report.Passed || len(report.Blockers) == 0 || !strings.Contains(errs.String(), "deadline exceeded") {
		t.Fatalf("exit=%d report=%+v stderr=%s", code, report, errs.String())
	}
}
