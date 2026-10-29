package managedrealtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/apid/receivers"
	"github.com/onebox-faas/faas/pkg/realtimepush"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

type realtimePushRegistration = receivers.RealtimePushRegistration

// RegisterRealtimePush (bound to a Service) satisfies receivers.PushRegistrar
// for the realtime fleet's inbox operations.
var _ receivers.PushRegistrar = (*Service)(nil).RegisterRealtimePush

func pushProviderLabel(ep, provider string) string {
	return api.RealtimePushProviderNamespacePrefix + ep + ":" + provider
}
func pushDeviceLabel(ep, principal, device string) string {
	pk, _ := state.ManagedRealtimeReadPrincipalKey(principal)
	return api.RealtimePushDeviceNamespacePrefix + ep + ":" + pk + ":" + device
}
func (s *Service) sealPush(value any, label string, max int) ([]byte, error) {
	recipients, err := s.Host.SecretRecipients()
	if err != nil {
		return nil, errors.New("push sealing unavailable")
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return secretbox.SealBytesTo(recipients, label, data, max)
}
func (s *Service) openPush(ctx context.Context, sealed []byte, label string, value any) error {
	ns, data, err := secretbox.OpenBytesMulti(s.Host.HostIdentitiesForUnseal(ctx), sealed)
	if err != nil {
		return err
	}
	if ns != label {
		return errors.New("push credential namespace mismatch")
	}
	return json.Unmarshal(data, value)
}
func (s *Service) RegisterRealtimePush(ctx context.Context, store state.ManagedRealtimePushStore, ep, principal, device string, registration realtimePushRegistration) error {
	if err := realtimepush.ValidateTarget(registration.Provider, registration.Target); err != nil {
		return state.ErrManagedRealtimeHistoryInvalid
	}
	if registration.Provider == "webpush" {
		if err := s.Host.ResolveAndCheckEgress(ctx, registration.Target.Endpoint); err != nil {
			return state.ErrManagedRealtimeHistoryInvalid
		}
	}
	raw, _ := json.Marshal(registration)
	fingerprint := sha256.Sum256(raw)
	sealed, err := s.sealPush(registration.Target, pushDeviceLabel(ep, principal, device), api.RealtimePushTargetSealMaxBytes)
	if err != nil {
		return err
	}
	return store.PutManagedRealtimePushDevice(ctx, state.ManagedRealtimePushDevice{EndpointID: ep, Principal: principal, Device: device, Provider: registration.Provider, Enabled: true, Sealed: sealed, Fingerprint: hex.EncodeToString(fingerprint[:])})
}
func (s *Service) ManagedRealtimePush(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if s.previewUnavailable(w, acct.ID, s.HistoryPreviewEnabled, "push preview unavailable") {
		return
	}
	ep, _, ok := s.loadManagedRealtimeEndpoint(w, r, acct)
	if !ok {
		return
	}
	store, ok := s.Host.Store().(state.ManagedRealtimePushStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("push unavailable"))
		return
	}
	var err error
	if collection := r.PathValue("push_collection"); collection != "" && collection != "providers" && collection != "devices" && collection != "deliveries" {
		httpkit.NotFound(w, "push collection unavailable")
		return
	}
	if provider := r.PathValue("provider"); provider != "" {
		if r.Method == http.MethodPut {
			var req struct {
				Config  realtimepush.Config `json:"config"`
				Enabled *bool               `json:"enabled,omitempty"`
			}
			if httpkit.DecodeJSONSized(r, &req, 32768) != nil || req.Config.Provider != provider || realtimepush.ValidateConfig(req.Config) != nil {
				api.WriteProblem(w, api.ErrRealtimeInvalid("invalid push provider configuration"))
				return
			}
			sealed, e := s.sealPush(req.Config, pushProviderLabel(ep.ID, provider), api.RealtimePushConfigSealMaxBytes)
			if e != nil {
				api.WriteProblem(w, api.ErrCapacity("push credential sealing unavailable"))
				return
			}
			enabled := req.Enabled == nil || *req.Enabled
			err = store.PutManagedRealtimePushProvider(r.Context(), state.ManagedRealtimePushProvider{EndpointID: ep.ID, Provider: provider, Enabled: enabled, Sealed: sealed})
			if err == nil {
				s.Host.EmitAudit(r.Context(), "realtime.push_provider_updated", &acct.ID, map[string]any{"endpoint_id": ep.ID, "provider": provider, "enabled": enabled})
				httpkit.WriteJSON(w, http.StatusOK, map[string]any{"provider": provider, "enabled": enabled})
				return
			}
		}
	} else if r.PathValue("push_collection") == "providers" {
		result, e := store.ListManagedRealtimePushProviders(r.Context(), ep.ID)
		err = e
		if err == nil {
			httpkit.WriteJSON(w, http.StatusOK, result)
			return
		}
	} else {
		principal := r.URL.Query().Get("principal")
		if api.ValidateRealtimePrincipal(principal) != nil {
			api.WriteProblem(w, api.ErrRealtimeInvalid("invalid principal"))
			return
		}
		device := r.PathValue("device")
		switch {
		case r.Method == http.MethodPut:
			if !ep.Enabled {
				api.WriteProblem(w, api.ErrRealtimeInvalid("endpoint must be enabled"))
				return
			}
			var req realtimePushRegistration
			if httpkit.DecodeJSONSized(r, &req, 32768) != nil {
				api.WriteProblem(w, api.ErrRealtimeInvalid("invalid push registration"))
				return
			}
			err = s.RegisterRealtimePush(r.Context(), store, ep.ID, principal, device, req)
		case r.Method == http.MethodDelete:
			err = store.DeleteManagedRealtimePushDevice(r.Context(), ep.ID, principal, device)
		case r.PathValue("push_collection") == "deliveries":
			result, e := store.ListManagedRealtimePushDeliveries(r.Context(), ep.ID, principal)
			err = e
			if err == nil {
				httpkit.WriteJSON(w, http.StatusOK, result)
				return
			}
		default:
			result, e := store.ListManagedRealtimePushDevices(r.Context(), ep.ID, principal)
			err = e
			if err == nil {
				httpkit.WriteJSON(w, http.StatusOK, result)
				return
			}
		}
		if err == nil {
			s.Host.EmitAudit(r.Context(), "realtime.push_device_updated", &acct.ID, map[string]any{"endpoint_id": ep.ID, "device": device, "removed": r.Method == http.MethodDelete})
			httpkit.WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
			return
		}
	}
	if err == nil {
		api.WriteProblem(w, api.ErrRealtimeInvalid("invalid push operation"))
		return
	}
	s.writeRealtimeInboxError(w, r, err)
}
