// adr: 566
package objectstorageactivity_test

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/objectstorageactivity"
	"github.com/onebox-faas/faas/pkg/state"
)

type mutationStore struct {
	active    int
	finishErr error
}

func (s *mutationStore) BeginObjectBucketMutation(context.Context, state.ObjectBucket, string) (state.ObjectBucketMutation, error) {
	s.active++
	return state.ObjectBucketMutation{ID: "receipt"}, nil
}
func (s *mutationStore) FinishObjectBucketMutation(context.Context, state.ObjectBucketMutation) error {
	if s.finishErr != nil {
		return s.finishErr
	}
	s.active--
	return nil
}

func TestExecutePreservesProviderEvidenceAndUncertainWriter(t *testing.T) {
	uncertain := errors.New("provider reply lost")
	for _, tc := range []struct {
		name                   string
		providerErr, finishErr error
		active                 int
	}{
		{name: "success"},
		{name: "provider_uncertain", providerErr: uncertain, active: 1},
		{name: "acknowledgement_lost", finishErr: uncertain, active: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := &mutationStore{finishErr: tc.finishErr}
			evidence, err := objectstorageactivity.Execute(t.Context(), st, state.ObjectBucket{}, func(callCtx context.Context) (string, error) {
				if callCtx != t.Context() || st.active != 1 {
					t.Fatal("provider ran outside the recorded writer")
				}
				return "immutable-provider-version", tc.providerErr
			})
			if evidence != "immutable-provider-version" || st.active != tc.active {
				t.Fatalf("evidence=%q active=%d", evidence, st.active)
			}
			wantErr := tc.providerErr != nil || tc.finishErr != nil
			if wantErr != errors.Is(err, uncertain) {
				t.Fatalf("dispatch error=%v", err)
			}
		})
	}
}
