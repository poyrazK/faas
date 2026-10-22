package main_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func saveProductPilotEvidence(t *testing.T, c main.ProductCommand, path string, raw []byte) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(filepath.Dir((*c.File())), path), raw, 0600); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:])
}

func saveProductPilotInbox(t *testing.T, c main.ProductCommand, step *api.ProductPilotJourneyStep, page api.ProductNotificationPage) {
	t.Helper()
	raw, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	step.EvidenceSHA256 = saveProductPilotEvidence(t, c, step.EvidenceFile, raw)
}

func addCompleteProductPilotJourneys(t *testing.T, f *main.ProductPilotFixture, m *api.ProductPilotManifest, c main.ProductCommand) []api.ProductNotificationPage {
	t.Helper()
	addProductPilotJourneys(t, f, m, c)
	m.Version = 2
	var pages []api.ProductNotificationPage
	for i := range m.Cases {
		item, j := &m.Cases[i], &m.Journeys[i]
		item.OutputFile = item.Name + ".csv"
		item.OutputSHA256 = saveProductPilotEvidence(t, c, item.OutputFile, []byte("id,value\n1,PRIVATE_CSV\n"))
		h := (*f.Help())[j.HelpRequestID]
		at := h.CreatedAt.Add(-4 * time.Minute).Truncate(time.Microsecond)
		op := (*f.Operations())[j.OperationID]
		op.Evidence.ResolvedAt, op.Evidence.SucceededAt = &at, &at
		(*f.Operations())[j.OperationID] = op
		now := h.UpdatedAt.Add(time.Second)
		page := api.ProductNotificationPage{AccountID: uuid.NewString(), AppID: m.AppID, TenantID: j.TenantID, ReadStateScope: "workspace", ObservedAt: now, WindowStart: now.Add(-api.ProductControlNotificationWindow), UnreadCount: 2, Items: []api.ProductNotification{
			{ID: "work/" + j.OperationID, ReferenceID: j.OperationID, Kind: "work_succeeded", Feature: m.Feature, OccurredAt: at, Revision: strings.Repeat("c", 64)},
			{ID: "help/" + j.HelpRequestID, ReferenceID: j.HelpRequestID, Kind: "help_resolved", HelpKind: "run", OccurredAt: h.UpdatedAt.Truncate(time.Microsecond), Revision: strings.Repeat("d", 64)},
		}}
		step := api.ProductPilotJourneyStep{Stage: "inbox_update", Outcome: "observed", ObservedAt: now.Add(time.Second), EvidenceFile: item.Name + "-inbox.json"}
		saveProductPilotInbox(t, c, &step, page)
		j.Steps = append(j.Steps, step)
		pages = append(pages, page)
	}
	return pages
}

func TestProductPilotCompleteJourneyRequiresCurrentInboxSources(t *testing.T) {
	for _, name := range []string{"complete", "missing stage", "missing work", "stale help", "wrong work outcome", "blocked observation", "changed artifact", "split pages"} {
		t.Run(name, func(t *testing.T) {
			f, m, c := main.NewProductPilotFixture(t)
			pages := addCompleteProductPilotJourneys(t, f, &m, c)
			j, page := &m.Journeys[0], pages[0]
			step := &j.Steps[6]
			switch name {
			case "missing stage":
				j.Steps = j.Steps[:6]
			case "missing work":
				page.Items = page.Items[1:]
				saveProductPilotInbox(t, c, step, page)
			case "stale help":
				page.Items[1].OccurredAt = page.Items[1].OccurredAt.Add(-time.Second)
				saveProductPilotInbox(t, c, step, page)
			case "wrong work outcome":
				page.Items[0].Kind = "work_failed"
				saveProductPilotInbox(t, c, step, page)
			case "blocked observation":
				step.Outcome = "blocked"
				step.EvidenceSHA256 = saveProductPilotEvidence(t, c, step.EvidenceFile, []byte("Reduced browser failure evidence"))
			case "changed artifact":
				_ = saveProductPilotEvidence(t, c, step.EvidenceFile, []byte("changed after review"))
			case "split pages":
				second := *step
				second.EvidenceFile = "second-page.json"
				page.Items = pages[0].Items[:1]
				saveProductPilotInbox(t, c, step, page)
				page.Items = pages[0].Items[1:]
				saveProductPilotInbox(t, c, &second, page)
				j.Steps = append(j.Steps, second)
			}
			main.SaveProductPilot(t, (*c.File()), m)
			r, err := main.VerifyProductPilot(t.Context(), f, c)
			want, exit := "incomplete", 3
			if name == "complete" || name == "split pages" {
				want, exit = "recorded", 0
			} else if name == "changed artifact" {
				want, exit = "blocked", 1
			}
			if err != nil || r.Version != 2 || r.JourneyState != want || main.ProductReportExit(r) != exit || r.QualificationState != "unverified" {
				t.Fatalf("journey result: %+v %v", r, err)
			}
			raw, err := json.Marshal(r)
			if err != nil || strings.Contains(string(raw), strings.Repeat("d", 64)) || strings.Contains(string(raw), "PRIVATE_CSV") {
				t.Fatal("inbox or CSV contents escaped into report", err)
			}
			for _, check := range r.Checks {
				if check.Impact == "qualification" && check.Status != "unverified" {
					t.Fatal("recorded artifacts qualified a live browser")
				}
			}
		})
	}
}

func TestProductPilotInboxRejectsUntrustedPageEvidence(t *testing.T) {
	for _, name := range []string{"app", "tenant", "account", "reference", "revision", "read version", "window", "expired item", "future item", "future page", "unread count", "scope", "duplicate item", "unknown field", "duplicate JSON", "trailing JSON", "oversize"} {
		t.Run(name, func(t *testing.T) {
			f, m, c := main.NewProductPilotFixture(t)
			pages := addCompleteProductPilotJourneys(t, f, &m, c)
			page, step := pages[0], &m.Journeys[0].Steps[6]
			switch name {
			case "app":
				page.AppID = uuid.NewString()
			case "tenant":
				page.TenantID = m.Cases[1].TenantID
			case "account":
				page.AccountID = uuid.Nil.String()
			case "reference":
				page.Items[0].ReferenceID = uuid.NewString()
			case "revision":
				page.Items[0].Revision = "not-reviewed"
			case "read version":
				page.Items[0].Read = true
			case "window":
				page.WindowStart = page.WindowStart.Add(time.Second)
			case "expired item":
				page.Items[0].OccurredAt = page.WindowStart
			case "future item":
				page.Items[0].OccurredAt = step.ObservedAt.Add(time.Second)
			case "future page":
				page.ObservedAt = step.ObservedAt.Add(time.Second)
				page.WindowStart = page.ObservedAt.Add(-api.ProductControlNotificationWindow)
			case "unread count":
				page.UnreadCount = 0
			case "scope":
				page.ReadStateScope = "person"
			case "duplicate item":
				page.Items[1] = page.Items[0]
			}
			raw, err := json.Marshal(page)
			if err != nil {
				t.Fatal(err)
			}
			switch name {
			case "unknown field":
				raw = append([]byte(`{"PRIVATE_SECRET":"must-not-appear",`), raw[1:]...)
			case "duplicate JSON":
				raw = append([]byte(`{"tenant_id":"PRIVATE_SECRET",`), raw[1:]...)
			case "trailing JSON":
				raw = append(raw, []byte(`{"PRIVATE_SECRET":true}`)...)
			case "oversize":
				raw = append(raw, []byte(strings.Repeat(" ", api.ProductControlNotificationResponseMaxBytes))...)
			}
			step.EvidenceSHA256 = saveProductPilotEvidence(t, c, step.EvidenceFile, raw)
			main.SaveProductPilot(t, (*c.File()), m)
			if _, err := main.VerifyProductPilot(t.Context(), f, c); err == nil || strings.Contains(err.Error(), "PRIVATE_SECRET") {
				t.Fatal("untrusted page accepted or its contents included in error", err)
			}
		})
	}
}

func TestProductPilotCompleteManifestCannotSilentlyFallBack(t *testing.T) {
	for _, name := range []string{"missing customer", "missing CSV", "other run help", "empty CSV"} {
		t.Run(name, func(t *testing.T) {
			f, m, c := main.NewProductPilotFixture(t)
			_ = addCompleteProductPilotJourneys(t, f, &m, c)
			switch name {
			case "missing customer":
				m.Journeys = m.Journeys[:2]
			case "missing CSV":
				m.Cases[0].OutputFile, m.Cases[0].OutputSHA256 = "", ""
			case "other run help":
				m.HelpCases[0].Kind, m.HelpCases[0].OperationID = "workspace", ""
			case "empty CSV":
				m.Cases[0].OutputSHA256 = saveProductPilotEvidence(t, c, m.Cases[0].OutputFile, nil)
			}
			main.SaveProductPilot(t, (*c.File()), m)
			if _, err := main.VerifyProductPilot(t.Context(), f, c); err == nil || (name != "empty CSV" && (*f.Reads()) != 0) {
				t.Fatal("incomplete manifest accepted or invalid references reached API", err)
			}
		})
	}
}
