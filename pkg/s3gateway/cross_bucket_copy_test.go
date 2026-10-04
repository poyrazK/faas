package s3gateway

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

type selectorCopySources struct {
	Store
	state.ObjectS3CopySourceStore
	source  state.ObjectBucket
	grant   state.ObjectS3CopySource
	revoked bool
	calls   int
}

func (s *selectorCopySources) ResolveObjectS3CopySource(_ context.Context, account, credential, source, key string) (state.ObjectS3CopySource, state.ObjectBucket, error) {
	s.calls++
	if s.revoked || account != s.grant.AccountID || credential != s.grant.CredentialID || source != s.source.ID || key != "allowed/key" {
		return state.ObjectS3CopySource{}, state.ObjectBucket{}, state.ErrNotFound
	}
	return s.grant, s.source, nil
}

// adr: 420
func TestCopySourceUUIDPrecedence(t *testing.T) {
	account, destination, source, credential := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, tc := range []struct {
		name, bucketName, selector, permission string
		revoked, cross                         bool
		status, resolutions                    int
	}{
		{"UUID name collision", source, source, state.ObjectBucketPermissionWrite, false, true, 200, 1},
		{"part UUID name collision", source, source, state.ObjectBucketPermissionWrite, false, true, 200, 1},
		{"revoked collision", source, source, state.ObjectBucketPermissionReadWrite, true, false, 404, 1},
		{"own bucket ID still needs read", source, destination, state.ObjectBucketPermissionWrite, false, false, 403, 0},
		{"legacy name", "assets", "assets", state.ObjectBucketPermissionReadWrite, false, false, 200, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := &selectorCopySources{source: state.ObjectBucket{ID: source}, grant: state.ObjectS3CopySource{ID: uuid.NewString(), AccountID: account, CredentialID: credential}, revoked: tc.revoked}
			h := &Handler{store: st}
			req := requestContext{bucket: state.ObjectBucket{ID: destination, Name: tc.bucketName, AccountID: account}, credential: state.ObjectS3Credential{ID: credential, Permission: tc.permission}, provider: &objectstorage.S3{}}
			w := httptest.NewRecorder()
			resolved, ok := h.authorizeGatewayCopySource(w, httptest.NewRequest("PUT", "/destination", nil), req, gatewayCopySource{Bucket: tc.selector, Key: "allowed/key"}, tc.name == "part UUID name collision")
			if ok != (tc.status == 200) || w.Code != tc.status || st.calls != tc.resolutions || (resolved.copySource != nil) != tc.cross {
				t.Fatal("wrong source interpretation", ok, w.Code, st.calls, resolved.copySource)
			}
			if tc.cross && (resolved.copySource.ID != source || resolved.copyGrantID != st.grant.ID) {
				t.Fatal("lost owned source authority", resolved)
			}
		})
	}
}
