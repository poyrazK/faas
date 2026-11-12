package object

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) lifecyclePolicyService(w http.ResponseWriter, r *http.Request, acct state.Account) (state.ObjectBucket, objectstorage.LifecyclePolicyService, bool) {
	w.Header().Set("Cache-Control", "no-store")
	b, _, ok := s.loadBucketRecord(w, r, acct)
	if !ok || !s.AuthorizeBucketData(w, r, b, state.ObjectBucketPermissionWrite) {
		return b, objectstorage.LifecyclePolicyService{}, false
	}
	st, ok := s.Host.Store().(state.ObjectLifecycleStore)
	if !ok {
		BucketProblem(w, objectstorage.ErrUnsupported)
		return b, objectstorage.LifecyclePolicyService{}, false
	}
	svc := objectstorage.LifecyclePolicyService{Store: st}
	if r.Method == http.MethodPut {
		if !s.ObjectStorageEnabled() {
			BucketProblem(w, ErrObjectStorageDisabled)
			return b, svc, false
		}
		backend, err := s.Host.ObjectStorage().Resolve(b.BackendID, b.BackendFingerprint)
		if err != nil {
			BucketProblem(w, err)
			return b, svc, false
		}
		svc.Provider = backend.Provider
	}
	return b, svc, true
}
func (s *Service) ObjectBucketLifecycle(w http.ResponseWriter, r *http.Request, acct state.Account) {
	b, svc, ok := s.lifecyclePolicyService(w, r, acct)
	if !ok {
		return
	}
	if r.URL.RawQuery != "" {
		BucketProblem(w, objectstorage.ErrInvalid)
		return
	}
	if r.Method != http.MethodPut && r.ContentLength != 0 {
		BucketProblem(w, objectstorage.ErrInvalid)
		return
	}
	if r.Method == http.MethodGet {
		p, err := svc.Read(r.Context(), b)
		if err != nil {
			BucketProblem(w, err)
			return
		}
		httpkit.WriteJSON(w, http.StatusOK, p.ObjectBucketLifecycle)
		return
	}
	var rules []api.ObjectLifecycleRule
	if r.Method == http.MethodPut {
		var good bool
		rules, good = decodeControlLifecycle(w, r)
		if !good {
			return
		}
	}
	p, err := svc.Write(r.Context(), b, rules)
	if err != nil {
		BucketProblem(w, err)
		return
	}
	s.Host.EmitAudit(r.Context(), "object_storage.lifecycle_policy_changed", &acct.ID, map[string]any{"bucket_id": b.ID, "revision": p.Revision, "rules": len(p.Rules)})
	httpkit.WriteJSON(w, http.StatusOK, p.ObjectBucketLifecycle)
}
func decodeControlLifecycle(w http.ResponseWriter, r *http.Request) ([]api.ObjectLifecycleRule, bool) {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, api.MaxObjectLifecycleBodyBytes))
	d.DisallowUnknownFields()
	var in api.ObjectBucketLifecycleRequest
	var extra any
	if d.Decode(&in) != nil || in.Rules == nil || len(in.Rules) == 0 || d.Decode(&extra) != io.EOF {
		BucketProblem(w, objectstorage.ErrInvalid)
		return nil, false
	}
	return in.Rules, true
}
func (s *Service) CreateObjectLifecycleScan(w http.ResponseWriter, r *http.Request, acct state.Account) {
	b, svc, ok := s.lifecyclePolicyService(w, r, acct)
	if !ok {
		return
	}
	if !s.ObjectStorageEnabled() {
		BucketProblem(w, ErrObjectStorageDisabled)
		return
	}
	if r.ContentLength != 0 || r.URL.RawQuery != "" {
		BucketProblem(w, objectstorage.ErrInvalid)
		return
	}
	j, err := svc.Store.StartObjectLifecycleScan(r.Context(), b.AccountID, b.AppID, b.ID)
	if errors.Is(err, state.ErrConflict) {
		lifecycleScanRefused(w, r, svc.Store, b)
		return
	}
	if err != nil {
		BucketProblem(w, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusAccepted, j.ObjectLifecycleScan)
}

// lifecycleScanRefused says why a scan cannot start.
func lifecycleScanRefused(w http.ResponseWriter, r *http.Request, st state.ObjectLifecycleStore, b state.ObjectBucket) {
	p, err := st.GetObjectBucketLifecycle(r.Context(), b.AccountID, b.AppID, b.ID)
	if err != nil {
		BucketProblem(w, err)
		return
	}
	api.WriteProblem(w, lifecycleScanRefusal(p))
}

// lifecycleScanRefusal names the reason StartObjectLifecycleScan conflicts: the
// policy has no enabled rule, or the last discovery ran too recently.
func lifecycleScanRefusal(p state.ObjectLifecyclePolicy) *api.Problem {
	for _, rule := range p.Rules {
		if rule.Status == "Enabled" {
			return api.NewProblem(http.StatusConflict, "lifecycle_scan_not_due", http.StatusText(http.StatusConflict), "Discovery ran recently; the next scan can start at "+p.NextScanAt.UTC().Format(time.RFC3339)+".")
		}
	}
	return api.NewProblem(http.StatusConflict, "lifecycle_no_enabled_rules", http.StatusText(http.StatusConflict), "The bucket has no enabled lifecycle rule to scan for; set rules with `gregale bucket lifecycle set` first.")
}
func (s *Service) GetObjectLifecycleScan(w http.ResponseWriter, r *http.Request, acct state.Account) {
	b, svc, ok := s.lifecyclePolicyService(w, r, acct)
	if !ok {
		return
	}
	id, err := uuid.Parse(r.PathValue("scan"))
	if err != nil || id.String() != r.PathValue("scan") {
		BucketProblem(w, state.ErrNotFound)
		return
	}
	if r.URL.RawQuery != "" || r.ContentLength != 0 {
		BucketProblem(w, objectstorage.ErrInvalid)
		return
	}
	j, err := svc.Store.GetObjectLifecycleScan(r.Context(), b.AccountID, b.ID, id.String())
	if err != nil {
		BucketProblem(w, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, j.ObjectLifecycleScan)
}
