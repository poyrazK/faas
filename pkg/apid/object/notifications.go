package object

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) ObjectBucketNotifications(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	b, _, ok := s.loadBucketRecord(w, r, acct)
	if !ok || !s.AuthorizeBucketData(w, r, b, state.ObjectBucketPermissionWrite) {
		return
	}
	st, ok := s.Host.Store().(state.ObjectNotificationStore)
	if !ok {
		BucketProblem(w, objectstorage.ErrUnsupported)
		return
	}
	if r.URL.RawQuery != "" || r.Method != http.MethodPut && r.ContentLength != 0 {
		BucketProblem(w, objectstorage.ErrInvalid)
		return
	}
	if r.Method == http.MethodGet {
		out, err := st.GetObjectBucketNotifications(r.Context(), b.AccountID, b.AppID, b.ID)
		if err != nil {
			BucketProblem(w, err)
			return
		}
		httpkit.WriteJSON(w, http.StatusOK, out)
		return
	}
	s.writeObjectBucketNotifications(w, r, acct, b, st)
}
func (s *Service) writeObjectBucketNotifications(w http.ResponseWriter, r *http.Request, acct state.Account, b state.ObjectBucket, st state.ObjectNotificationStore) {
	rules := []api.ObjectNotificationRule{}
	if r.Method == http.MethodPut {
		d := json.NewDecoder(http.MaxBytesReader(w, r.Body, api.MaxObjectNotificationBodyBytes))
		d.DisallowUnknownFields()
		var in api.ObjectBucketNotificationsRequest
		var extra any
		if d.Decode(&in) != nil || in.Rules == nil || d.Decode(&extra) != io.EOF {
			BucketProblem(w, objectstorage.ErrInvalid)
			return
		}
		rules = in.Rules
		if len(rules) > 0 && !s.ObjectStorageEnabled() {
			BucketProblem(w, objectstorage.ErrUnavailable)
			return
		}
	}
	out, err := st.SetObjectBucketNotifications(r.Context(), b.AccountID, b.AppID, b.ID, rules)
	if errors.Is(err, state.ErrObjectNotificationInvalid) {
		api.WriteProblem(w, api.ErrValidation("invalid notification rules or destination"))
		return
	}
	if err != nil {
		BucketProblem(w, err)
		return
	}
	s.Host.EmitAudit(r.Context(), "object_storage.notifications_changed", &acct.ID, map[string]any{"bucket_id": b.ID, "revision": out.Revision, "rules": len(out.Rules)})
	httpkit.WriteJSON(w, http.StatusOK, out)
}
