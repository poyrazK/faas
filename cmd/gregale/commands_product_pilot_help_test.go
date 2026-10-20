package main_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/productcontrol"
)

func addProductPilotHelp(t *testing.T, f *main.ProductPilotFixture, m *api.ProductPilotManifest) {
	t.Helper()
	(*f.Help()) = map[string]api.ProductHelpRequest{}
	start := time.Now().UTC().Add(-time.Hour)
	for _, c := range m.Cases {
		id := uuid.NewString()
		value := api.ProductHelpRequest{AccountID: uuid.NewString(), AppID: m.AppID, TenantID: c.TenantID, Request: api.ProductHelpCreate{ID: id, Kind: "run", OperationID: c.OperationID, Message: "CUSTOMER_PRIVATE_TEXT"}, Context: api.ProductHelpContext{ObservedAt: start.Add(6 * time.Minute), Revision: c.Revision, PolicyRevision: c.Revision, Plan: c.Plan, Period: (*f.Operations())[c.OperationID].Evidence.Receipt.Period, Reason: "run_reported", Feature: m.Feature}, Status: "resolved", Response: "OWNER_PRIVATE_TEXT", Version: 2, CreatedAt: start.Add(6 * time.Minute), UpdatedAt: start.Add(10 * time.Minute)}
		message, response, captured, err := productcontrol.PilotHelpHashes(value)
		if err != nil {
			t.Fatal(err)
		}
		m.HelpCases = append(m.HelpCases, api.ProductPilotHelpCase{Name: c.Name + "-help", TenantID: c.TenantID, RequestID: id, Kind: "run", OperationID: c.OperationID, ExpectedStatus: "resolved", ExpectedVersion: 2, MessageSHA256: message, ResponseSHA256: response, ContextSHA256: captured})
		(*f.Help())[id] = value
	}
}

func TestProductPilotHelpReadOnlyPrivacyAndRecoveryEvidence(t *testing.T) {
	f, m, c := main.NewProductPilotFixture(t)
	addProductPilotHelp(t, f, &m)
	main.SaveProductPilot(t, (*c.File()), m)
	r, err := main.VerifyProductPilot(t.Context(), f, c)
	if err != nil || r.HelpState != "observed" || r.LedgerState != "observed" || len(r.Help) != 3 || main.ProductReportExit(r) != 0 {
		t.Fatalf("help evidence: %+v %v", r, err)
	}
	raw, err := json.Marshal(r)
	if err != nil || strings.Contains(string(raw), "CUSTOMER_PRIVATE_TEXT") || strings.Contains(string(raw), "OWNER_PRIVATE_TEXT") {
		t.Fatal("report contains help text", err)
	}
	for _, check := range r.Checks {
		if check.Impact == "qualification" && check.Status != "unverified" {
			t.Fatal("owner help reads qualified customer visibility/recovery")
		}
	}
	again, err := main.VerifyProductPilot(t.Context(), f, c)
	if err != nil || again.ManifestSHA256 != r.ManifestSHA256 || again.HelpState != "observed" {
		t.Fatal("read-only verification changed retained evidence", err)
	}
}

func TestProductPilotHelpInspectionRequiresScopeAndReturnsOnlyHashes(t *testing.T) {
	f, m, c := main.NewProductPilotFixture(t)
	addProductPilotHelp(t, f, &m)
	(*c.Tenant()), (*c.HelpID()) = m.HelpCases[0].TenantID, m.HelpCases[0].RequestID
	args := []string{"pilot", "help", "--app", (*c.App()), "--tenant", (*c.Tenant()), "--request", (*c.HelpID())}
	if _, err := main.ParseProductCommand(args); err != nil {
		t.Fatal(err)
	}
	r, err := main.InspectProductPilotHelp(t.Context(), f, c)
	if err != nil || r.Result != "inspection_only" || r.MessageSHA256 != m.HelpCases[0].MessageSHA256 || r.ContextSHA256 != m.HelpCases[0].ContextSHA256 {
		t.Fatalf("help inspection: %+v %v", r, err)
	}
	raw, err := json.Marshal(r)
	if err != nil || strings.Contains(string(raw), "CUSTOMER_PRIVATE_TEXT") || strings.Contains(string(raw), "OWNER_PRIVATE_TEXT") {
		t.Fatal("inspection contains text", err)
	}
	(*c.Tenant()) = m.Cases[1].TenantID
	if _, err := main.InspectProductPilotHelp(t.Context(), f, c); err == nil {
		t.Fatal("inspection accepted another customer's request")
	}
	for _, invalid := range [][]string{args[:5], {"pilot", "help", "--app", (*c.App()), "--tenant", "other", "--request", (*c.HelpID())}} {
		if _, err := main.ParseProductCommand(invalid); err == nil {
			t.Fatal("incomplete/invalid inspection scope accepted")
		}
	}
}

func TestProductPilotHelpResponseGuards(t *testing.T) {
	for _, name := range []string{"app", "tenant", "request", "status", "version", "message", "response", "context", "context terms", "malformed"} {
		t.Run(name, func(t *testing.T) {
			f, m, c := main.NewProductPilotFixture(t)
			addProductPilotHelp(t, f, &m)
			id := m.HelpCases[0].RequestID
			value := (*f.Help())[id]
			switch name {
			case "app":
				value.AppID = uuid.NewString()
			case "tenant":
				value.TenantID = uuid.NewString()
			case "request":
				value.Request.ID = uuid.NewString()
			case "status":
				value.Status = "in_progress"
			case "version":
				value.Version++
			case "message":
				value.Request.Message = "changed"
			case "response":
				value.Response = "changed"
			case "context":
				value.Context.Plan = "changed"
			case "context terms":
				value.Context.Plan = "changed"
				_, _, hash, err := productcontrol.PilotHelpHashes(value)
				if err != nil {
					t.Fatal(err)
				}
				m.HelpCases[0].ContextSHA256 = hash
			case "malformed":
				value.UpdatedAt = value.CreatedAt.Add(-time.Second)
			}
			(*f.Help())[id] = value
			main.SaveProductPilot(t, (*c.File()), m)
			r, err := main.VerifyProductPilot(t.Context(), f, c)
			if name == "app" || name == "tenant" || name == "request" || name == "malformed" {
				if err == nil || len(r.Help) != 0 {
					t.Fatal("untrusted help response emitted into report")
				}
			} else if err != nil || r.HelpState != "blocked" || r.LedgerState != "observed" || main.ProductReportExit(r) != 1 {
				t.Fatalf("help mismatch did not block pilot independently of ledger: %+v %v", r, err)
			}
		})
	}
}

func TestProductPilotHelpManifestRejectsForeignReferencesBeforeReads(t *testing.T) {
	for _, name := range []string{"tenant", "run", "duplicate", "hash", "kind", "version", "bound"} {
		t.Run(name, func(t *testing.T) {
			f, m, c := main.NewProductPilotFixture(t)
			addProductPilotHelp(t, f, &m)
			switch name {
			case "tenant":
				m.HelpCases[0].TenantID = uuid.NewString()
			case "run":
				m.HelpCases[0].OperationID = m.Cases[1].OperationID
			case "duplicate":
				m.HelpCases[1].RequestID = m.HelpCases[0].RequestID
			case "hash":
				m.HelpCases[0].ContextSHA256 = "unreviewed"
			case "kind":
				m.HelpCases[0].Kind = "workspace"
			case "version":
				m.HelpCases[0].ExpectedVersion = 0
			case "bound":
				for len(m.HelpCases) <= api.ProductControlPilotMaxCases {
					m.HelpCases = append(m.HelpCases, m.HelpCases[0])
				}
			}
			main.SaveProductPilot(t, (*c.File()), m)
			if _, err := main.VerifyProductPilot(t.Context(), f, c); err == nil || (*f.Reads()) != 0 {
				t.Fatal("invalid help manifest reached the API")
			}
		})
	}
}
