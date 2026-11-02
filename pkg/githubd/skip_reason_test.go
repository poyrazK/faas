// adr: 20261101160300
package githubd

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// A delivery that deployed nothing records why, and every reason githubd
// records has a customer-facing description (ADR-20261101160300,
// amendment 2).
func TestSkipReason(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{nil, ""},
		{errors.New("source fetch failed"), ""},
		{pushSkip(SkipReasonActionsTrigger), SkipReasonActionsTrigger},
		{fmt.Errorf("wrapped: %w", pushSkip(SkipReasonSuperseded)), SkipReasonSuperseded},
		{ErrIgnored, SkipReasonIgnored},
		{ErrNoBinding, SkipReasonNoBinding},
		{ErrSkipDeploy, SkipReasonSkipMarker},
		{releaseTagRejection{reason: releaseTagReasonMoved}, releaseTagReasonMoved},
	} {
		if got := SkipReason(tc.err); got != tc.want {
			t.Errorf("SkipReason(%v) = %q, want %q", tc.err, got, tc.want)
		}
	}
	if !IsIgnored(pushSkip(SkipReasonBranchNotDeployed)) || !errors.Is(pushSkip(SkipReasonBranchNotDeployed), ErrIgnored) {
		t.Fatal("a typed skip is no longer an ignored push")
	}
	for _, reason := range []string{
		SkipReasonActionsTrigger, SkipReasonBranchNotDeployed, SkipReasonSuperseded, SkipReasonNotEnabled,
		SkipReasonTagNotBranchHead, SkipReasonNoBinding, SkipReasonSkipMarker, SkipReasonIgnored,
		releaseTagReasonInvalid, releaseTagReasonMoved,
	} {
		if api.GitHubSkipReasonDescription(reason) == "" {
			t.Errorf("skip reason %q has no description", reason)
		}
	}
}

// skipRecordingStore records how deliveries complete.
type skipRecordingStore struct {
	WebhookDeliveryStore
	reasons map[string]string
}

func (s *skipRecordingStore) CompleteSkipped(_ context.Context, deliveryID, reason string) error {
	s.reasons[deliveryID] = reason
	return nil
}

func TestCompleteWebhookDeliveryRecordsReason(t *testing.T) {
	store := &skipRecordingStore{reasons: map[string]string{}}
	if err := completeWebhookDelivery(context.Background(), store, "d1", SkipReason(pushSkip(SkipReasonActionsTrigger))); err != nil {
		t.Fatal(err)
	}
	if err := completeWebhookDelivery(context.Background(), store, "d2", SkipReason(nil)); err != nil {
		t.Fatal(err)
	}
	if store.reasons["d1"] != SkipReasonActionsTrigger || store.reasons["d2"] != "" {
		t.Fatalf("recorded reasons = %v", store.reasons)
	}
	// A store without the reason column completes as before.
	plain := &completeOnlyStore{}
	if err := completeWebhookDelivery(context.Background(), plain, "d3", SkipReasonSuperseded); err != nil || plain.completed != "d3" {
		t.Fatalf("plain store completed %q, %v", plain.completed, err)
	}
}

type completeOnlyStore struct {
	WebhookDeliveryStore
	completed string
}

func (s *completeOnlyStore) Complete(_ context.Context, deliveryID string) error {
	s.completed = deliveryID
	return nil
}
