package state

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// The copy lands paused, publication proves it, an explicit write or a resume
// clears the pause, and a paused bucket's events reach no rule
// (ADR-20261104120000 amendment 2026-11-16).
func TestMemCloneBucketNotificationsCopyPausedAndResume(t *testing.T) {
	ctx := context.Background()
	rule := api.ObjectNotificationRule{ID: "on-put", Destination: "arn:gregale:lambda:us-east-1:00000000-0000-0000-0000-000000000001:function:00000000-0000-0000-0000-000000000002", Events: []string{"s3:ObjectCreated:Put"}}
	captured := &ProjectEnvironmentCloneObjectNotifications{Revision: 3, Rules: []api.ObjectNotificationRule{rule}, ManagedRuleIDs: []string{"on-put"}}
	source := ProjectEnvironmentCloneObjectBucket{ID: "source", Notifications: captured}

	m := NewMemStore()
	if m.projectEnvironmentCloneOperations == nil {
		m.projectEnvironmentCloneOperations = map[string]ProjectEnvironmentCloneOperation{}
	}
	if m.objectBuckets == nil {
		m.objectBuckets = map[string]ObjectBucket{}
	}
	m.projectEnvironmentCloneOperations["op"] = ProjectEnvironmentCloneOperation{ID: "op", AccountID: "acct", ProjectID: "proj", TargetEnvironment: "stage"}
	m.projectEnvironments["env"] = ProjectEnvironment{ID: "env", AccountID: "acct", ProjectID: "proj", Slug: "stage"}
	m.objectBuckets["target"] = ObjectBucket{ID: "target", AccountID: "acct", Scope: "stage", State: "ready",
		EnvironmentCloneOperationID: "op", EnvironmentCloneSourceBucketID: "source"}
	m.copyCloneBucketNotificationsLocked(source, "target")
	if p := m.objectNotifications["target"]; p.Revision != 3 || len(p.Rules) != 1 || len(p.ManagedRuleIDs) != 1 || !m.objectNotificationClonePaused["target"] {
		t.Fatalf("copy = %+v paused=%v", p, m.objectNotificationClonePaused["target"])
	}
	record := projectCloneWorkloadRecord{snapshot: projectCloneWorkloadSnapshot{Bindings: &ProjectEnvironmentCloneBindingDefinitions{Buckets: []ProjectEnvironmentCloneObjectBucket{source}}}}
	op := m.projectEnvironmentCloneOperations["op"]
	if err := m.verifyCloneBucketNotificationPublicationLocked(op, []projectCloneWorkloadRecord{record}); err != nil {
		t.Fatalf("proof of the paused copy: %v", err)
	}

	// A paused bucket's events reach no rule.
	if recipients, err := notificationRecipients(m.objectBuckets["target"], notificationEnvironment{}, api.ObjectBucketNotifications{}, "s3:ObjectCreated:Put", api.ObjectStorageEvent{}, m.notificationQueueLocked); err != nil || len(recipients) != 0 {
		t.Fatalf("an empty policy produced recipients: %+v, %v", recipients, err)
	}

	resumed, err := m.ResumeEnvironmentClonePausedBucketNotifications(ctx, "acct", "proj", "stage")
	if err != nil || len(resumed) != 1 || resumed[0] != "target" || m.objectNotificationClonePaused["target"] {
		t.Fatalf("resume = %v, %v", resumed, err)
	}
	if err := m.verifyCloneBucketNotificationPublicationLocked(op, []projectCloneWorkloadRecord{record}); !errors.Is(err, ErrConflict) {
		t.Fatalf("proof accepted an unpaused copy: %v", err)
	}
	if again, err := m.ResumeEnvironmentClonePausedBucketNotifications(ctx, "acct", "proj", "stage"); err != nil || len(again) != 0 {
		t.Fatalf("a second resume = %v, %v", again, err)
	}
	if _, err := m.ResumeEnvironmentClonePausedBucketNotifications(ctx, "acct", "proj", "production"); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("production resume = %v", err)
	}
	if _, err := m.ResumeEnvironmentClonePausedBucketNotifications(ctx, "acct", "proj", "nowhere"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing environment resume = %v", err)
	}
}

func TestValidateCloneNotificationCopy(t *testing.T) {
	rule := api.ObjectNotificationRule{ID: "r", Destination: "d", Events: []string{"s3:ObjectCreated:Put"}}
	source := &ProjectEnvironmentCloneObjectNotifications{Revision: 1, Rules: []api.ObjectNotificationRule{rule}}
	stored := clonedObjectNotifications("t", source)
	if err := validateCloneNotificationCopy(source, stored, true, true); err != nil {
		t.Fatalf("matching paused copy: %v", err)
	}
	for name, c := range map[string]struct {
		present, paused bool
		target          api.ObjectBucketNotifications
	}{
		"missing":   {false, false, api.ObjectBucketNotifications{BucketID: "t"}},
		"unpaused":  {true, false, stored},
		"different": {true, true, api.ObjectBucketNotifications{BucketID: "t", Revision: 1}},
	} {
		if err := validateCloneNotificationCopy(source, c.target, c.present, c.paused); !errors.Is(err, ErrConflict) {
			t.Fatalf("%s copy accepted: %v", name, err)
		}
	}
	if err := validateCloneNotificationCopy(nil, api.ObjectBucketNotifications{}, false, false); err != nil {
		t.Fatalf("no capture, no copy: %v", err)
	}
	if err := validateCloneNotificationCopy(nil, stored, true, false); !errors.Is(err, ErrConflict) {
		t.Fatalf("a copy without a capture accepted: %v", err)
	}
}

func TestRequireCloneNotificationDestinations(t *testing.T) {
	const app = "00000000-0000-0000-0000-000000000002"
	queueRule := api.ObjectNotificationRule{ID: "q", Destination: "arn:gregale:sqs:us-east-1:00000000-0000-0000-0000-000000000001:" + app + "/images"}
	record := func(queues []string, rules ...api.ObjectNotificationRule) projectCloneWorkloadRecord {
		r := projectCloneWorkloadRecord{snapshot: projectCloneWorkloadSnapshot{
			Bindings: &ProjectEnvironmentCloneBindingDefinitions{Buckets: []ProjectEnvironmentCloneObjectBucket{{ID: "b", Notifications: &ProjectEnvironmentCloneObjectNotifications{Rules: rules}}}},
			Policies: &projectCloneScopedPolicies{Queues: &ProjectEnvironmentCloneQueueDefinitions{}}}}
		for _, q := range queues {
			r.snapshot.Policies.Queues.Bindings = append(r.snapshot.Policies.Queues.Bindings, ProjectEnvironmentCloneQueueBinding{ProjectEnvironmentQueueDefinition: ProjectEnvironmentQueueDefinition{QueueName: q}})
		}
		r.AppID = app
		return r
	}
	if err := requireCloneNotificationDestinations([]projectCloneWorkloadRecord{record([]string{"images"}, queueRule)}); err != nil {
		t.Fatalf("a captured queue refused: %v", err)
	}
	compact := record([]string{"images"}, queueRule)
	compact.AppID = "00000000000000000000000000000002" // The memory store's form.
	if err := requireCloneNotificationDestinations([]projectCloneWorkloadRecord{compact}); err != nil {
		t.Fatalf("a compact app ID refused: %v", err)
	}
	if err := requireCloneNotificationDestinations([]projectCloneWorkloadRecord{record(nil, queueRule)}); !errors.Is(err, ErrProjectEnvironmentCloneObjectNotificationDestinationUnavailable) {
		t.Fatalf("a queue the stage will not hold accepted: %v", err)
	}
}
