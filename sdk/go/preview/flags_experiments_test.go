package preview

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"
)

// ADR 20261119173702 §1: a rule_match on a subject experiment queues one
// exposure per flag, customer, subject and variant an hour, and Flush sends
// it with the events.
func TestSubjectExperimentExposures(t *testing.T) {
	raw, err := os.ReadFile("../../../pkg/flags/testdata/decisions.json")
	if err != nil {
		t.Fatal(err)
	}
	var vectors struct {
		Bundle json.RawMessage `json:"bundle"`
	}
	if err := json.Unmarshal(raw, &vectors); err != nil {
		t.Fatal(err)
	}
	bundle, err := validateRuntimeBundle(vectors.Bundle)
	if err != nil {
		t.Fatal(err)
	}
	if !bundle.subjectExperiment("subject-checkout", "by-user") || bundle.subjectExperiment("layered-checkout", "weighted") {
		t.Fatal("subjectExperiment must report only the subject experiment's rule")
	}

	flags, _ := newTrackTestFlags(t, 200, -1)
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	d := FlagDecision{Flag: "subject-checkout", Value: "new", Type: "variant", RuleID: "by-user", Reason: "rule_match"}
	flags.recordExposure(d, testFlagCustomer, "user-7", now)
	flags.recordExposure(d, testFlagCustomer, "user-7", now.Add(30*time.Minute))
	flags.recordExposure(d, testFlagCustomer, "user-8", now)
	flags.recordExposure(d, testFlagCustomer, "user-7", now.Add(61*time.Minute))
	if n := len(flags.events.exposures); n != 3 {
		t.Fatalf("queued %d exposures; want 3 (one repeat inside the hour is dropped)", n)
	}
	if got := flags.events.exposures[0]; got.Subject != "user-7" || got.Variant != "new" || got.RuleID != "by-user" || got.CustomerID != testFlagCustomer {
		t.Fatalf("exposure = %+v", got)
	}
	if err := flags.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := len(flags.events.exposures); n != 0 {
		t.Fatalf("%d exposures left after Flush", n)
	}
}
