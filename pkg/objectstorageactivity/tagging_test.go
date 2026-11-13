package objectstorageactivity_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/objectstorageactivity"
	"github.com/onebox-faas/faas/pkg/state"
)

type activityTagger struct {
	objectstorage.Provider
	write func() error
}

func (p activityTagger) GetObjectTags(context.Context, string, string) (map[string]string, error) {
	return map[string]string{}, nil
}
func (p activityTagger) PutObjectTags(context.Context, string, string, map[string]string) error {
	return p.write()
}
func (p activityTagger) DeleteObjectTags(context.Context, string, string) error { return p.write() }

func TestTagRecordsOnlyPreparedMutations(t *testing.T) {
	for _, method := range []string{http.MethodPut, http.MethodDelete} {
		for _, tc := range []struct {
			name                                                   string
			admitErr, requestErr, providerErr, finishErr, beginErr error
			key                                                    string
			active, calls                                          int
		}{
			{name: "success", key: "key", calls: 1},
			{name: "invalid key"},
			{name: "authorization refusal", key: "key", admitErr: state.ErrObjectBudget},
			{name: "native request refusal", key: "key", requestErr: state.ErrObjectBudget},
			{name: "capture held", key: "key", beginErr: state.ErrObjectBucketWriteFenced},
			{name: "definite native rejection", key: "key", providerErr: errors.Join(objectstorage.ErrWriteRejected, objectstorage.ErrConflict), calls: 1},
			{name: "lost provider reply", key: "key", providerErr: objectstorage.ErrUnavailable, active: 1, calls: 1},
			{name: "lost completion", key: "key", finishErr: state.ErrConflict, active: 1, calls: 1},
		} {
			t.Run(method+tc.name, func(t *testing.T) {
				st := &mutationStore{finishErr: tc.finishErr, beginErr: tc.beginErr}
				calls := 0
				service := objectstorage.TaggingService{Provider: activityTagger{write: func() error {
					calls++
					if st.active != 1 {
						t.Error("provider mutation had no writer")
					}
					return tc.providerErr
				}}, BeforeOperation: func(context.Context) error {
					if st.active != 0 {
						t.Error("writer preceded operation admission")
					}
					return tc.admitErr
				}, BeforeRequest: func(context.Context) error {
					if st.active != 0 {
						t.Error("writer preceded request admission")
					}
					return tc.requestErr
				}}
				_, err := objectstorageactivity.Tag(t.Context(), st, state.ObjectBucket{}, service, method, tc.key, "", nil)
				if st.active != tc.active || calls != tc.calls || (err == nil) != (tc.name == "success") {
					t.Fatal("incorrect writer custody", st.active, calls, err)
				}
			})
		}
	}
}

type activityVersionTagger struct {
	objectstorage.Provider
	write func() (objectstorage.ObjectTaggingResult, error)
}

func (p activityVersionTagger) GetObjectVersionTags(context.Context, string, string, string) (objectstorage.ObjectTaggingResult, error) {
	return p.write()
}
func (p activityVersionTagger) PutObjectVersionTags(context.Context, string, string, string, map[string]string) (objectstorage.ObjectTaggingResult, error) {
	return p.write()
}
func (p activityVersionTagger) DeleteObjectVersionTags(context.Context, string, string, string) (objectstorage.ObjectTaggingResult, error) {
	return p.write()
}

type failingTagReferences struct {
	state.ObjectVersionReferenceStore
}

func (failingTagReferences) RecordObjectVersions(context.Context, string, string, []state.ObjectVersionIdentity) ([]state.ObjectVersionIdentity, error) {
	return nil, state.ErrConflict
}

func TestTagKeepsWriterWhenPublicVersionPersistenceFails(t *testing.T) {
	st := &mutationStore{}
	writes := 0
	service := objectstorage.TaggingService{References: failingTagReferences{}, Provider: activityVersionTagger{write: func() (objectstorage.ObjectTaggingResult, error) {
		writes++
		if st.active != 1 {
			t.Error("unrecorded native mutation")
		}
		return objectstorage.ObjectTaggingResult{ProviderVersionID: "native-version"}, nil
	}}}
	_, err := objectstorageactivity.Tag(t.Context(), st, state.ObjectBucket{}, service, http.MethodPut, "key", "", nil)
	if !errors.Is(err, objectstorage.ErrUnavailable) || writes != 1 || st.active != 1 {
		t.Fatal("unacknowledged public identity released writer", err, writes, st.active)
	}
}
