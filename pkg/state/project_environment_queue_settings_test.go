// adr: 590
package state

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/google/uuid"
)

func TestEnvironmentQueueSettingsSurviveOrdinaryWorkloadEdits(t *testing.T) {
	for _, complete := range []bool{false, true} {
		book := queueSettingsFixture()
		if !complete {
			book.Bindings = []ProjectEnvironmentQueueDefinition{}
		}
		settings, err := cloneWorkloadSettings(ProjectEnvironmentWorkloadSettings{Type: AppTypeApp, WorkloadClass: WorkloadClassWorker, RAMMB: 256, QueueBindings: &book})
		if err != nil {
			t.Fatal(err)
		}
		ram := 512
		updated, err := ApplyWorkloadSettingsUpdate(settings, UpdateAppParams{RAMMB: &ram})
		if err != nil || updated.RAMMB != ram || !reflect.DeepEqual(updated.QueueBindings, settings.QueueBindings) {
			t.Fatalf("ordinary settings edit dropped queues: %+v, %v", updated, err)
		}
		updated.QueueBindings.Revision++
		if updated.QueueBindings.Revision == settings.QueueBindings.Revision {
			t.Fatal("edited book aliases original")
		}
	}
}

func queueSettingsFixture() ProjectEnvironmentQueueSettings {
	return ProjectEnvironmentQueueSettings{Revision: 1, Bindings: []ProjectEnvironmentQueueDefinition{
		{Name: "orders", QueueName: "orders", Mode: "pull", WorkloadClass: WorkloadClassWorker, Enabled: true, MaxConcurrency: 3, RetryPolicyJSON: []byte(`{"max_attempts":3,"max_seconds":20,"base_seconds":1}`)},
	}}
}

func TestEnvironmentQueueSettingsRejectInvalidConfiguration(t *testing.T) {
	for _, fault := range []struct {
		name string
		edit func(*ProjectEnvironmentQueueSettings)
	}{
		{"clock", func(s *ProjectEnvironmentQueueSettings) { s.Revision = 0 }},
		{"duplicate_name", func(s *ProjectEnvironmentQueueSettings) { s.Bindings = append(s.Bindings, s.Bindings[0]) }},
		{"duplicate_queue", func(s *ProjectEnvironmentQueueSettings) {
			b := s.Bindings[0]
			b.Name = "another"
			s.Bindings = append(s.Bindings, b)
		}},
		{"mode", func(s *ProjectEnvironmentQueueSettings) { s.Bindings[0].Mode = "future" }},
		{"class", func(s *ProjectEnvironmentQueueSettings) { s.Bindings[0].WorkloadClass = WorkloadClass("future") }},
		{"pull_http", func(s *ProjectEnvironmentQueueSettings) { s.Bindings[0].WorkloadClass = WorkloadClassHTTP }},
		{"negative_concurrency", func(s *ProjectEnvironmentQueueSettings) { s.Bindings[0].MaxConcurrency = -1 }},
		{"retry_shape", func(s *ProjectEnvironmentQueueSettings) { s.Bindings[0].RetryPolicyJSON = []byte(`[]`) }},
		{"unknown_retry", func(s *ProjectEnvironmentQueueSettings) { s.Bindings[0].RetryPolicyJSON = []byte(`{"future":1}`) }},
		{"invalid_backoff", func(s *ProjectEnvironmentQueueSettings) {
			s.Bindings[0].RetryPolicyJSON = []byte(`{"base_seconds":10,"max_seconds":1}`)
		}},
		{"trailing_json", func(s *ProjectEnvironmentQueueSettings) { s.Bindings[0].RetryPolicyJSON = []byte(`{} {}`) }},
	} {
		t.Run(fault.name, func(t *testing.T) {
			book := queueSettingsFixture()
			fault.edit(&book)
			if _, err := WorkloadSettingsHash(ProjectEnvironmentWorkloadSettings{QueueBindings: &book}); !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("invalid queue setting accepted: %v", err)
			}
		})
	}
}

func TestEnvironmentQueueSettingsCanonicalHashAndDefensiveCopies(t *testing.T) {
	book := queueSettingsFixture()
	settings := ProjectEnvironmentWorkloadSettings{QueueBindings: &book}
	before, _ := json.Marshal(settings)
	copy, err := cloneWorkloadSettings(settings)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(settings)
	if !bytes.Equal(before, after) {
		t.Fatal("normalization mutated input")
	}
	first, err := WorkloadSettingsHash(settings)
	if err != nil {
		t.Fatal(err)
	}
	copy.QueueBindings.Bindings[0].RetryPolicyJSON = []byte(`{"base_seconds":1,"max_seconds":20,"max_attempts":3}`)
	if second, err := WorkloadSettingsHash(copy); err != nil || second != first {
		t.Fatalf("JSON ordering changed queue config hash: %v", err)
	}
	copy.QueueBindings.Bindings[0].MaxConcurrency++
	if second, err := WorkloadSettingsHash(copy); err != nil || second == first {
		t.Fatalf("queue edit absent from config hash: %v", err)
	}
	if book.Bindings[0].MaxConcurrency != 3 {
		t.Fatal("returned queue configuration aliases input")
	}
	legacy, _ := json.Marshal(ProjectEnvironmentWorkloadSettings{})
	if bytes.Contains(legacy, []byte("queue_bindings")) {
		t.Fatal("legacy settings encoding changed")
	}
	for _, book := range []ProjectEnvironmentQueueSettings{{Revision: 1}, {Revision: 1, Bindings: []ProjectEnvironmentQueueDefinition{}}} {
		copy, err := cloneWorkloadSettings(ProjectEnvironmentWorkloadSettings{QueueBindings: &book})
		if err != nil || copy.QueueBindings.Bindings == nil {
			t.Fatalf("complete empty vanished: %+v, %v", copy, err)
		}
	}
}

func TestCloneQueueCatalogueRejectsBrokenIdentityAndPublication(t *testing.T) {
	book := queueSettingsFixture()
	queues := ProjectEnvironmentCloneQueueDefinitions{Version: 1, AppID: uuid.NewString(), SourceScope: "production", Bindings: []ProjectEnvironmentCloneQueueBinding{{SourceID: uuid.NewString(), ProjectEnvironmentQueueDefinition: book.Bindings[0]}}}
	for _, owned := range []bool{false, true} {
		copy := queues
		copy.EnvironmentOwned = owned
		if owned {
			copy.Bindings = append([]ProjectEnvironmentCloneQueueBinding{}, queues.Bindings...)
			copy.Bindings[0].SourceID = ""
		}
		if _, err := normalizeCloneQueues(copy); err != nil {
			t.Fatal(err)
		}
		record := projectCloneWorkloadRecord{snapshot: projectCloneWorkloadSnapshot{Policies: &projectCloneScopedPolicies{Queues: &copy}, Settings: ProjectEnvironmentWorkloadSettings{QueueBindings: &book}}}
		if err := validateCloneScopedPolicyPublication(record, *record.snapshot.Policies, true); !errors.Is(err, ErrProjectEnvironmentQueueActivationUnavailable) {
			t.Fatalf("catalogue equality proved a consumer: %v", err)
		}
	}
	queues.Bindings[0].SourceID = ""
	if _, err := normalizeCloneQueues(queues); !errors.Is(err, ErrConflict) {
		t.Fatalf("legacy remapping identity omitted: %v", err)
	}
	if _, err := qualificationWorkloadHash(ProjectEnvironmentWorkloadSpec{Settings: ProjectEnvironmentWorkloadSettings{QueueBindings: &ProjectEnvironmentQueueSettings{Revision: 1}}}, ProjectEnvironmentWorkloadSpec{}, ProjectEnvironmentWorkloadSettings{}); !errors.Is(err, ErrProjectEnvironmentQueueActivationUnavailable) {
		t.Fatalf("empty queue settings qualified: %v", err)
	}
}
