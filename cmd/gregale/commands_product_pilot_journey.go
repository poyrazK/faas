package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/onebox-faas/faas/pkg/api"
)

func verifyProductPilotJourneys(directory string, m api.ProductPilotManifest, r *api.ProductPilotReport) error {
	if len(m.Journeys) == 0 {
		return nil
	}
	r.JourneyState = "recorded"
	for _, j := range m.Journeys {
		value, err := verifyProductPilotJourney(directory, j, r)
		if err != nil {
			return err
		}
		r.Journeys = append(r.Journeys, value)
		if value.State == "blocked" || (value.State == "incomplete" && r.JourneyState != "blocked") {
			r.JourneyState = value.State
		}
	}
	if len(m.Journeys) != api.ProductControlPilotMaxJourneys && r.JourneyState != "blocked" {
		r.JourneyState = "incomplete"
	}
	r.Checks = append(r.Checks, api.ProductDoctorCheck{Check: "customer_journeys", Status: "unverified", Impact: "qualification", Code: "operator_recorded_journeys", Message: "Local artifacts and operator-recorded timings were compared with selected run/help references. These records do not independently establish browser behavior, real users or native qualification."})
	return nil
}

func verifyProductPilotJourney(directory string, j api.ProductPilotJourney, r *api.ProductPilotReport) (api.ProductPilotJourneyResult, error) {
	v := api.ProductPilotJourneyResult{Name: j.Name, TenantID: j.TenantID, OperationID: j.OperationID, HelpRequestID: j.HelpRequestID, State: "recorded", Steps: j.Steps, Evidence: []api.ProductPilotOutput{}}
	stages := map[string]bool{}
	inboxWork, inboxHelp := false, false
	for i, step := range j.Steps {
		if step.ObservedAt.After(r.ObservedAt) {
			return v, fmt.Errorf("journey %s has an observation in the future", j.Name)
		}
		evidence, body, err := readProductPilotJourneyEvidence(directory, fmt.Sprintf("%s/%d", j.Name, i), step.EvidenceFile, step.Stage == "inbox_update" && step.Outcome == "observed")
		if err != nil {
			return v, err
		}
		v.Evidence = append(v.Evidence, evidence)
		if evidence.SHA256 != step.EvidenceSHA256 {
			v.State = "blocked"
			continue
		}
		if step.Outcome == "blocked" {
			v.BlockedSteps++
			continue
		}
		if step.Stage == "inbox_update" {
			work, help, err := verifyProductPilotInbox(body, j, step.ObservedAt, r)
			if err != nil {
				return v, err
			}
			inboxWork, inboxHelp = inboxWork || work, inboxHelp || help
		}
		stages[step.Stage] = true
		if step.Stage == "paid_feature" && v.FirstSuccessMS == nil {
			ms := step.ObservedAt.Sub(j.StartedAt).Milliseconds()
			v.FirstSuccessMS = &ms
		}
	}
	if v.State != "blocked" && (!pilotJourneySourcesObserved(j, r) || !stages["sign_in"] || !stages["paid_feature"] || !stages["usage"] || !stages["plan_change"] || !stages["help_request"] || !stages["owner_response"]) {
		v.State = "incomplete"
	}
	if r.Version == 2 && v.State != "blocked" && (!stages["inbox_update"] || !inboxWork || !inboxHelp) {
		v.State = "incomplete"
	}
	for _, help := range r.Help {
		if help.RequestID == j.HelpRequestID && help.Result == "observed" && help.Status == "resolved" {
			ms := help.UpdatedAt.Sub(help.CreatedAt).Milliseconds()
			v.HelpResolutionMS = &ms
		}
	}
	return v, nil
}

func pilotJourneySourcesObserved(j api.ProductPilotJourney, r *api.ProductPilotReport) bool {
	if r.LedgerState != "observed" {
		return false
	}
	success, replied := false, false
	for _, op := range r.Operations {
		if op.Evidence.OperationID == j.OperationID && op.Evidence.ResolvedState == api.OperationSucceeded && op.Evidence.SucceededAt != nil {
			success = true
		}
	}
	for _, help := range r.Help {
		if help.RequestID == j.HelpRequestID && help.Result == "observed" && help.ResponseSHA256 != emptyPilotTextSHA256() {
			replied = true
		}
	}
	return success && replied
}

func emptyPilotTextSHA256() string {
	hash := sha256.Sum256(nil)
	return hex.EncodeToString(hash[:])
}

func readProductPilotJourneyEvidence(directory, name, path string, includeBody bool) (api.ProductPilotOutput, []byte, error) {
	v := api.ProductPilotOutput{Case: name, File: path, Source: "operator_recorded_local_journey_artifact"}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return v, nil, err
	}
	defer func() { _ = root.Close() }()
	info, err := root.Stat(path)
	if err != nil {
		return v, nil, err
	}
	if !info.Mode().IsRegular() || info.Size() == 0 || info.Size() > api.ProductControlPilotJourneyEvidenceMaxBytes {
		return v, nil, errors.New("journey evidence must be a nonempty bounded regular file")
	}
	f, err := root.Open(path)
	if err != nil {
		return v, nil, fmt.Errorf("read journey evidence %s: %w", name, err)
	}
	defer func() { _ = f.Close() }()
	info, err = f.Stat()
	if err != nil {
		return v, nil, err
	}
	if !info.Mode().IsRegular() || info.Size() == 0 || info.Size() > api.ProductControlPilotJourneyEvidenceMaxBytes {
		return v, nil, errors.New("journey evidence must be a nonempty bounded regular file")
	}
	hash := sha256.New()
	var body bytes.Buffer
	var writer io.Writer = hash
	bound := int64(api.ProductControlPilotJourneyEvidenceMaxBytes)
	if includeBody {
		bound = api.ProductControlNotificationResponseMaxBytes
		writer = io.MultiWriter(hash, &body)
	}
	v.Bytes, err = io.Copy(writer, io.LimitReader(f, bound+1))
	if err != nil {
		return v, nil, err
	}
	if v.Bytes > bound {
		return v, nil, errors.New("journey evidence exceeds its bound")
	}
	v.SHA256 = hex.EncodeToString(hash.Sum(nil))
	return v, body.Bytes(), nil
}
