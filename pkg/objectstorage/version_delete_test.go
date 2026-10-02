package objectstorage

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

type versionDeleteReferenceStub struct {
	state.ObjectVersionReferenceStore
	native string
	err    error
}

func (s versionDeleteReferenceStub) ResolveObjectVersion(_ context.Context, account, bucket, key, id string) (string, error) {
	if account != "account" || bucket != "bucket" || key != "key" || !state.ValidObjectVersionID(id) {
		return "", state.ErrNotFound
	}
	return s.native, s.err
}

type versionDeleteBaseOnly struct{ Provider }

func TestOwnedVersionDeletionAdmissionFailures(t *testing.T) {
	for _, tc := range []struct {
		name, native                string
		resolveErr, beforeErr, want error
		unsupported                 bool
	}{
		{name: "unowned selector", native: "native", resolveErr: state.ErrNotFound, want: state.ErrNotFound},
		{name: "corrupt mutable reference", native: "null", want: ErrUnavailable},
		{name: "empty reference", want: ErrUnavailable},
		{name: "metric persistence failed", native: "native", beforeErr: state.ErrConflict, want: state.ErrConflict},
		{name: "unsupported provider", native: "native", want: ErrUnsupported, unsupported: true},
		{name: "owned immutable", native: "native"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			p := historyTestProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(204) })).(Provider)
			if tc.unsupported {
				p = versionDeleteBaseOnly{p}
			}
			id := uuid.NewString()
			out, err := DeleteOwnedObjectVersion(t.Context(), versionDeleteReferenceStub{native: tc.native, err: tc.resolveErr}, p, state.ObjectBucket{ID: "bucket", AccountID: "account", PhysicalName: "physical"}, "key", id, func(context.Context) error { return tc.beforeErr })
			if !errors.Is(err, tc.want) || err != nil && calls.Load() != 0 || err == nil && (calls.Load() != 1 || out.VersionID != id) {
				t.Fatal(out, err, calls.Load())
			}
			raw, _ := json.Marshal(out)
			if strings.Contains(string(raw), "native") || strings.Contains(string(raw), "physical") {
				t.Fatal("private identity exposed", string(raw))
			}
		})
	}
}

type currentDeleteGuardStub struct {
	state.ObjectVersionInventoryStore
	state.ObjectBucketVersioningStore
	status state.ObjectVersionAccountingStatus
	phase  string
	err    error
}

func (s currentDeleteGuardStub) ObjectVersionAccountingStatus(context.Context, string, string) (state.ObjectVersionAccountingStatus, error) {
	return s.status, s.err
}
func (s currentDeleteGuardStub) GetObjectBucketVersioning(context.Context, string, string, string) (state.ObjectBucketVersioning, error) {
	var v state.ObjectBucketVersioning
	v.State = s.phase
	return v, nil
}
func TestCurrentDeleteAccountingGuard(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status state.ObjectVersionAccountingStatus
		phase  string
		want   error
	}{
		{"current", state.ObjectVersionAccountingStatus{Scope: state.ObjectInventoryCurrent}, "ready", nil},
		{"native", state.ObjectVersionAccountingStatus{Scope: state.ObjectInventoryAllVersions}, "ready", ErrUnsupported},
		{"observed", state.ObjectVersionAccountingStatus{VersionsObserved: true}, "ready", ErrUnsupported},
		{"native scan", state.ObjectVersionAccountingStatus{NativeScanActive: true}, "ready", ErrUnsupported},
		{"configuration intent", state.ObjectVersionAccountingStatus{}, "waiting", state.ErrConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := CheckCurrentObjectDelete(t.Context(), currentDeleteGuardStub{status: tc.status, phase: tc.phase}, state.ObjectBucket{}); !errors.Is(err, tc.want) {
				t.Fatal(err)
			}
		})
	}
	if err := CheckCurrentObjectDelete(t.Context(), currentDeleteGuardStub{err: state.ErrNotFound}, state.ObjectBucket{}); !errors.Is(err, state.ErrNotFound) {
		t.Fatal(err)
	}
}

func TestDeleteRequestRejectsIgnoredConditions(t *testing.T) {
	for _, name := range []string{"If-Match", "If-None-Match", "If-Unmodified-Since", "X-Amz-Mfa", "X-Amz-Bypass-Governance-Retention"} {
		r := httptest.NewRequest(http.MethodDelete, "/", nil)
		r.Header.Set(name, "predicate")
		if err := ValidateObjectDeleteRequest(r); !errors.Is(err, ErrUnsupported) {
			t.Fatal(name, err)
		}
	}
	r := httptest.NewRequest(http.MethodDelete, "/", strings.NewReader("body"))
	if err := ValidateObjectDeleteRequest(r); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
}
