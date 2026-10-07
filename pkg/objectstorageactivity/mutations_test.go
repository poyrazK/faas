// adr: 590
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
	beginErr  error
}

func (s *mutationStore) BeginObjectBucketMutation(context.Context, state.ObjectBucket, string) (state.ObjectBucketMutation, error) {
	if s.beginErr != nil {
		return state.ObjectBucketMutation{}, s.beginErr
	}
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

type multipartMutationStore struct {
	mutationStore
	receipt state.ObjectBucketMutation
	readErr error
}

func (s *multipartMutationStore) ReadObjectMultipartMutation(context.Context, state.ObjectMultipartUpload) (state.ObjectBucketMutation, error) {
	return s.receipt, s.readErr
}

func TestExecuteMultipartPreservesOriginalReceipt(t *testing.T) {
	b := state.ObjectBucket{ID: "bucket", AccountID: "account", AppID: "app", BackendID: "backend", BackendFingerprint: "fingerprint", PhysicalName: "original"}
	lost := errors.New("reply lost")
	for _, providerErr := range []error{nil, lost} {
		st := &multipartMutationStore{receipt: state.ObjectBucketMutation{ID: "original", Bucket: b}, mutationStore: mutationStore{active: 1}}
		value, err := objectstorageactivity.ExecuteMultipart(t.Context(), st, st, b, state.ObjectMultipartUpload{}, func(ctx context.Context) (string, error) {
			if ctx != t.Context() {
				t.Fatal("changed caller context")
			}
			return "provider-evidence", providerErr
		})
		if value != "provider-evidence" || !errors.Is(err, providerErr) || st.active != 1 {
			t.Fatal("provider ACK or lost reply retired original", value, err, st.active)
		}
	}
	for _, readErr := range []error{state.ErrConflict, state.ErrObjectBucketWriteFenced} {
		st := &multipartMutationStore{readErr: readErr}
		_, err := objectstorageactivity.ExecuteMultipart(t.Context(), st, st, b, state.ObjectMultipartUpload{}, func(context.Context) (string, error) { t.Fatal("unauthorized provider call"); return "", nil })
		if !errors.Is(err, readErr) || st.active != 0 {
			t.Fatal(err, st.active)
		}
	}
	st := &multipartMutationStore{receipt: state.ObjectBucketMutation{Bucket: b}}
	changed := b
	changed.PhysicalName = "other"
	if _, err := objectstorageactivity.ExecuteMultipart(t.Context(), st, st, changed, state.ObjectMultipartUpload{}, func(context.Context) (string, error) { t.Fatal("changed placement called provider"); return "", nil }); err == nil {
		t.Fatal("placement mismatch accepted")
	}
}

func TestExecuteMultipartLegacyCannotResumeThroughHold(t *testing.T) {
	for _, boundInterface := range []bool{false, true} {
		st := &multipartMutationStore{mutationStore: mutationStore{beginErr: state.ErrObjectBucketWriteFenced}, readErr: state.ErrNotFound}
		var journal any = struct{}{}
		if boundInterface {
			journal = st
		}
		_, err := objectstorageactivity.ExecuteMultipart(t.Context(), st, journal, state.ObjectBucket{}, state.ObjectMultipartUpload{}, func(context.Context) (string, error) { t.Fatal("legacy provider call escaped hold"); return "", nil })
		if !errors.Is(err, state.ErrObjectBucketWriteFenced) || st.active != 0 {
			t.Fatal(err, st.active)
		}
	}
}
