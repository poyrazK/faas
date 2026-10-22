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

func addProductPilotJourneys(t *testing.T, f *main.ProductPilotFixture, m *api.ProductPilotManifest, c main.ProductCommand) {
	t.Helper()
	addProductPilotHelp(t, f, m)
	for i, item := range m.Cases {
		help := m.HelpCases[i]
		start := (*f.Help())[help.RequestID].CreatedAt.Add(-6 * time.Minute)
		j := api.ProductPilotJourney{Name: item.Name + "-journey", TenantID: item.TenantID, OperationID: item.OperationID, HelpRequestID: help.RequestID, StartedAt: start}
		for n, stage := range []string{"sign_in", "paid_feature", "usage", "plan_change", "help_request", "owner_response"} {
			path := item.Name + "-" + stage + ".json"
			data := []byte(`{"fixture":"PRIVATE_ARTIFACT_CONTENT","stage":"` + stage + `"}`)
			if err := os.WriteFile(filepath.Join(filepath.Dir((*c.File())), path), data, 0600); err != nil {
				t.Fatal(err)
			}
			hash := sha256.Sum256(data)
			j.Steps = append(j.Steps, api.ProductPilotJourneyStep{Stage: stage, Outcome: "observed", ObservedAt: start.Add(time.Duration(n+1) * time.Minute), EvidenceFile: path, EvidenceSHA256: hex.EncodeToString(hash[:])})
		}
		m.Journeys = append(m.Journeys, j)
	}
}

func TestProductPilotJourneysMeasuredWithoutQualification(t *testing.T) {
	f, m, c := main.NewProductPilotFixture(t)
	addProductPilotJourneys(t, f, &m, c)
	j := &m.Journeys[0]
	blocked := j.Steps[1]
	blocked.Outcome = "blocked"
	blocked.ObservedAt = j.StartedAt.Add(75 * time.Second)
	j.Steps = append(j.Steps[:1], append([]api.ProductPilotJourneyStep{blocked}, j.Steps[1:]...)...)
	main.SaveProductPilot(t, (*c.File()), m)
	r, err := main.VerifyProductPilot(t.Context(), f, c)
	if err != nil || r.JourneyState != "recorded" || r.QualificationState != "unverified" || len(r.Journeys) != 3 || main.ProductReportExit(r) != 0 {
		t.Fatalf("journeys: %+v %v", r, err)
	}
	first := r.Journeys[0]
	if first.FirstSuccessMS == nil || *first.FirstSuccessMS != 120000 || first.HelpResolutionMS == nil || *first.HelpResolutionMS != 240000 || first.BlockedSteps != 1 {
		t.Fatalf("journey measurements: %+v", first)
	}
	raw, err := json.Marshal(r)
	if err != nil || strings.Contains(string(raw), "PRIVATE_ARTIFACT_CONTENT") {
		t.Fatal("artifact contents escaped into report", err)
	}
	for _, check := range r.Checks {
		if check.Impact == "qualification" && check.Status != "unverified" {
			t.Fatal("operator records qualified live scenarios")
		}
	}
}

func TestProductPilotJourneyIncompleteAndChangedArtifacts(t *testing.T) {
	for _, name := range []string{"missing customer", "missing stage", "no response", "changed artifact", "future observation"} {
		t.Run(name, func(t *testing.T) {
			f, m, c := main.NewProductPilotFixture(t)
			addProductPilotJourneys(t, f, &m, c)
			switch name {
			case "missing customer":
				m.Journeys = m.Journeys[:2]
			case "missing stage":
				m.Journeys[0].Steps = m.Journeys[0].Steps[:5]
			case "no response":
				id := m.HelpCases[0].RequestID
				value := (*f.Help())[id]
				value.Response = ""
				value.Status, value.Version, value.UpdatedAt = "open", 1, value.CreatedAt
				(*f.Help())[id] = value
				m.HelpCases[0].ExpectedStatus, m.HelpCases[0].ExpectedVersion = "open", 1
				m.HelpCases[0].ResponseSHA256 = main.EmptyPilotTextSHA256()
			case "changed artifact":
				path := filepath.Join(filepath.Dir((*c.File())), m.Journeys[0].Steps[0].EvidenceFile)
				if err := os.WriteFile(path, []byte("changed"), 0600); err != nil {
					t.Fatal(err)
				}
			case "future observation":
				m.Journeys[0].Steps[5].ObservedAt = time.Now().Add(time.Hour)
			}
			main.SaveProductPilot(t, (*c.File()), m)
			r, err := main.VerifyProductPilot(t.Context(), f, c)
			if name == "future observation" {
				if err == nil {
					t.Fatal("future observation accepted")
				}
			} else if name == "changed artifact" {
				if err != nil || r.JourneyState != "blocked" || main.ProductReportExit(r) != 1 {
					t.Fatalf("changed artifact accepted: %+v %v", r, err)
				}
			} else if err != nil || r.JourneyState != "incomplete" || main.ProductReportExit(r) != 3 {
				t.Fatalf("partial journey claimed completion: %+v %v", r, err)
			}
		})
	}
}

func TestProductPilotJourneyReferencesRejectBeforeReads(t *testing.T) {
	for _, name := range []string{"foreign customer", "foreign help", "foreign run", "duplicate customer", "time order", "unknown stage", "path", "hash", "step bound"} {
		t.Run(name, func(t *testing.T) {
			f, m, c := main.NewProductPilotFixture(t)
			addProductPilotJourneys(t, f, &m, c)
			switch name {
			case "foreign customer":
				m.Journeys[0].TenantID = uuid.NewString()
			case "foreign help":
				m.Journeys[0].HelpRequestID = m.HelpCases[1].RequestID
			case "foreign run":
				m.Journeys[0].OperationID = m.Cases[1].OperationID
			case "duplicate customer":
				m.Journeys[1] = m.Journeys[0]
				m.Journeys[1].Name += "-copy"
			case "time order":
				m.Journeys[0].Steps[1].ObservedAt = m.Journeys[0].StartedAt.Add(-time.Second)
			case "unknown stage":
				m.Journeys[0].Steps[0].Stage = "native_qualified"
			case "path":
				m.Journeys[0].Steps[0].EvidenceFile = "../private.json"
			case "hash":
				m.Journeys[0].Steps[0].EvidenceSHA256 = "unreviewed"
			case "step bound":
				for len(m.Journeys[0].Steps) <= api.ProductControlPilotMaxJourneySteps {
					m.Journeys[0].Steps = append(m.Journeys[0].Steps, m.Journeys[0].Steps[5])
				}
			}
			main.SaveProductPilot(t, (*c.File()), m)
			if _, err := main.VerifyProductPilot(t.Context(), f, c); err == nil || (*f.Reads()) != 0 {
				t.Fatal("invalid journey reached the API")
			}
		})
	}
}

func TestProductPilotJourneyEvidenceCannotEscapeOrReadSpecialFiles(t *testing.T) {
	for _, name := range []string{"symlink", "directory", "empty", "oversize"} {
		t.Run(name, func(t *testing.T) {
			f, m, c := main.NewProductPilotFixture(t)
			addProductPilotJourneys(t, f, &m, c)
			path := filepath.Join(filepath.Dir((*c.File())), m.Journeys[0].Steps[0].EvidenceFile)
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			switch name {
			case "symlink":
				outside := filepath.Join(t.TempDir(), "secret.json")
				if err := os.WriteFile(outside, []byte("private"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, path); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			case "empty", "oversize":
				file, err := os.Create(path)
				if err != nil {
					t.Fatal(err)
				}
				if name == "oversize" {
					if err := file.Truncate(api.ProductControlPilotJourneyEvidenceMaxBytes + 1); err != nil {
						t.Fatal(err)
					}
				}
				if err := file.Close(); err != nil {
					t.Fatal(err)
				}
			}
			main.SaveProductPilot(t, (*c.File()), m)
			if _, err := main.VerifyProductPilot(t.Context(), f, c); err == nil {
				t.Fatal("unsafe evidence file accepted")
			}
		})
	}
}
