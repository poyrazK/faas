// adr: 375
package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type PublicRoutingInputs struct {
	Scope, RequestedReleaseID, RequestedRevisionID, HostDeploymentID, HostScope, VersionKey string
	Valid, ResolveRelease, ReleasePresent, RevisionPresent, Async, Smoke                    bool
}

type PublicRoutingSnapshot struct {
	AppID, AccountID, Scope                                          string
	ReleaseRequested, ReleaseID, ReleaseDeploymentID, ReleaseVerdict string
	ReleaseResolved                                                  bool
	RevisionID                                                       string
	RevisionChecked, RevisionAllowed                                 bool
	HostDeploymentID, HostScope                                      string
	HostChecked, HostAllowed, Async                                  bool
	Weights                                                          []DeploymentWeightsRow
	SelectedDeploymentID                                             string `json:"-"`
	SelectionReason                                                  string `json:"-"`
}

type PublicRoutingPinner func(context.Context, App, PublicRoutingInputs) (PublicRoutingSnapshot, error)
type publicRoutingSnapshotKey struct{}

func (h *Handler) WithPublicRoutingPolicy(pin PublicRoutingPinner) *Handler {
	h.publicRoutingPolicy = pin
	return h
}

// Parse on a private header copy. The existing public pin gate owns error
// contracts, forwarded headers and browser cookie mutations after this read.
func publicRoutingInputs(r *http.Request, app App, versionKey, smoke string, async bool) PublicRoutingInputs {
	scope := app.Scope
	if scope == "" && app.ProjectID != "" && !app.IsPreview {
		scope = "production"
	}
	inputs := PublicRoutingInputs{Scope: scope, VersionKey: versionKey, Valid: true, Async: async,
		HostDeploymentID: app.PinnedDeploymentID, HostScope: app.PinnedDeploymentScope, Smoke: smoke != ""}
	if inputs.Smoke {
		inputs.HostDeploymentID, inputs.HostScope = smoke, app.Scope
		return inputs
	}
	copyRequest := r.Clone(r.Context())
	_, revisionPresent := copyRequest.Header[http.CanonicalHeaderKey(api.RevisionHeader)]
	_, releasePresent := copyRequest.Header[http.CanonicalHeaderKey(api.ReleaseHeader)]
	if isWebSocketHandshake(copyRequest) {
		protocol, present, invalid := consumeManagedReleaseSubprotocol(copyRequest)
		if invalid || present && (revisionPresent || releasePresent) {
			inputs.Valid = false
			return inputs
		}
		if present {
			copyRequest.Header.Set(api.ReleaseHeader, protocol)
			releasePresent = true
		}
		if app.ProjectID != "" && !app.IsPreview && app.PinnedDeploymentID == "" && !revisionPresent && !releasePresent {
			cookie, present, duplicate := managedReleaseContextCookieValue(copyRequest)
			if duplicate {
				inputs.Valid = false
				return inputs
			}
			if present {
				copyRequest.Header.Set(api.ReleaseHeader, cookie)
				releasePresent = true
			}
		}
	}
	inputs.ReleasePresent, inputs.RevisionPresent = releasePresent, revisionPresent
	if releasePresent && revisionPresent {
		inputs.Valid = false
		return inputs
	}
	if releasePresent {
		inputs.RequestedReleaseID, inputs.Valid = publicRoutingUUID(copyRequest.Header.Values(api.ReleaseHeader))
		if app.IsPreview || app.PinnedDeploymentID != "" {
			inputs.Valid = false
		}
	}
	if revisionPresent {
		inputs.RequestedRevisionID, inputs.Valid = publicRoutingUUID(copyRequest.Header.Values(api.RevisionHeader))
		if app.RevisionPinTTLSeconds <= 0 || app.PinnedDeploymentID != "" {
			inputs.Valid = false
		}
	}
	inputs.ResolveRelease = inputs.Valid && app.PinnedDeploymentID == "" && !app.IsPreview &&
		(releasePresent || app.ProjectID != "" && !revisionPresent && !async)
	return inputs
}

func publicRoutingUUID(values []string) (string, bool) {
	if len(values) != 1 || len(values[0]) != 36 {
		return "", false
	}
	id, err := uuid.Parse(values[0])
	if err != nil {
		return "", false
	}
	return id.String(), true
}

func (h *Handler) pinPublicRoutingPolicy(w http.ResponseWriter, r *http.Request, app App, inputs PublicRoutingInputs) bool {
	if h.publicRoutingPolicy == nil || !inputs.Valid {
		return false
	}
	defer measureTrafficPhase(r.Context(), trafficPolicy)()
	bounded, cancel := context.WithTimeout(r.Context(), api.TrafficPublicRoutingReadTimeout)
	defer cancel()
	source, err := h.publicRoutingPolicy(bounded, app, inputs)
	if err == nil {
		err = bounded.Err()
	}
	if err == nil {
		err = validatePublicRoutingSnapshot(source, app, inputs)
	}
	if err != nil {
		if handleForwardRequestCancellation(w, r, true) {
			return true
		}
		h.writeTrafficPolicyUnavailable(w, r)
		return true
	}
	encoded, err := json.Marshal(source)
	if err != nil {
		h.writeTrafficPolicyUnavailable(w, r)
		return true
	}
	var frozen PublicRoutingSnapshot
	if err := json.Unmarshal(encoded, &frozen); err != nil {
		h.writeTrafficPolicyUnavailable(w, r)
		return true
	}
	h.selectPublicRoutingDeployment(r, app, inputs, &frozen)
	if frozen.SelectedDeploymentID == "" && !inputs.Async && frozen.ReleaseVerdict != "gone" && frozen.ReleaseVerdict != "conflict" &&
		(!frozen.RevisionChecked || frozen.RevisionAllowed) {
		h.writeTrafficPolicyUnavailable(w, r)
		return true
	}
	proof, err := json.Marshal(struct {
		Base    string
		Routing json.RawMessage
	}{TrafficPolicyRevision(r.Context()), encoded})
	if err != nil {
		h.writeTrafficPolicyUnavailable(w, r)
		return true
	}
	ctx := context.WithValue(r.Context(), publicRoutingSnapshotKey{}, frozen)
	*r = *r.WithContext(context.WithValue(ctx, effectiveTrafficPolicyKey{}, policyDigest(proof)))
	return false
}

func validatePublicRoutingSnapshot(snapshot PublicRoutingSnapshot, app App, inputs PublicRoutingInputs) error {
	if snapshot.AppID != app.ID || snapshot.AccountID != app.AccountID || snapshot.Scope != inputs.Scope || snapshot.Async != inputs.Async ||
		snapshot.ReleaseResolved != inputs.ResolveRelease || snapshot.ReleaseRequested != inputs.RequestedReleaseID ||
		snapshot.RevisionID != inputs.RequestedRevisionID || snapshot.HostDeploymentID != inputs.HostDeploymentID || snapshot.HostScope != inputs.HostScope ||
		len(snapshot.Weights) > api.TrafficPolicyMaxDeployments {
		return errors.New("unverified public routing snapshot")
	}
	if inputs.RevisionPresent && !snapshot.RevisionChecked || inputs.HostDeploymentID != "" && !snapshot.HostChecked {
		return errors.New("missing public pin verdict")
	}
	if inputs.ResolveRelease && snapshot.ReleaseVerdict != "allowed" && snapshot.ReleaseVerdict != "gone" && snapshot.ReleaseVerdict != "conflict" {
		return errors.New("missing public release verdict")
	}
	seen := make(map[string]bool, len(snapshot.Weights))
	for _, weight := range snapshot.Weights {
		if weight.ID == "" || weight.TrafficPercent <= 0 || weight.TrafficPercent > api.TrafficPolicyMaxWeight || seen[weight.ID] {
			return errors.New("invalid public routing weights")
		}
		seen[weight.ID] = true
	}
	return nil
}

func (h *Handler) selectPublicRoutingDeployment(r *http.Request, app App, inputs PublicRoutingInputs, snapshot *PublicRoutingSnapshot) {
	switch {
	case snapshot.HostAllowed:
		snapshot.SelectedDeploymentID, snapshot.SelectionReason = snapshot.HostDeploymentID, "host"
	case snapshot.ReleaseVerdict == "gone" || snapshot.ReleaseVerdict == "conflict":
		return
	case snapshot.ReleaseDeploymentID != "":
		snapshot.SelectedDeploymentID, snapshot.SelectionReason = snapshot.ReleaseDeploymentID, "release"
	case snapshot.RevisionChecked:
		if snapshot.RevisionAllowed {
			snapshot.SelectedDeploymentID, snapshot.SelectionReason = snapshot.RevisionID, "revision"
		}
	case inputs.Async:
		return
	default:
		if inputs.VersionKey == "" && app.SessionAffinity {
			preferred := h.affinityTargetFromRequest(r, app.ID)
			if picker, ok := h.backend.(affinityPicker); ok && preferred != "" {
				candidate := picker.PickForInstance(app.ID, preferred)
				if candidate.OK && candidate.Target.InstanceID == preferred {
					for _, weight := range snapshot.Weights {
						if weight.ID == candidate.Target.DeploymentID {
							snapshot.SelectedDeploymentID, snapshot.SelectionReason = weight.ID, "session"
							return
						}
					}
				}
			}
		}
		key, reason := inputs.VersionKey, "version"
		if key == "" {
			key, reason = uuid.NewString(), "weighted"
		}
		snapshot.SelectedDeploymentID, _ = AffinityDeploymentFromWeights(app.ID, key, snapshot.Weights)
		snapshot.SelectionReason = reason
	}
}

func publicRoutingSnapshot(ctx context.Context) (PublicRoutingSnapshot, bool) {
	snapshot, ok := ctx.Value(publicRoutingSnapshotKey{}).(PublicRoutingSnapshot)
	return snapshot, ok
}

func pickPublicDeployment(backend Backend, app, deployment, preferred string) PickResult {
	if picker, ok := backend.(interface {
		PickForDeploymentInstance(string, string, string) PickResult
	}); ok {
		return picker.PickForDeploymentInstance(app, deployment, preferred)
	}
	if picker, ok := backend.(deploymentTargetPicker); ok {
		return picker.PickForDeployment(app, deployment)
	}
	return PickResult{}
}

func (h *Handler) resolvePublicProjectRelease(ctx context.Context, app, scope, requested string) (string, string, error) {
	if h.publicRoutingPolicy == nil {
		return h.backend.(projectReleaseResolver).ResolveProjectRelease(ctx, app, scope, requested)
	}
	snapshot, ok := publicRoutingSnapshot(ctx)
	if !ok || snapshot.AppID != app || snapshot.Scope != scope || !snapshot.ReleaseResolved || snapshot.ReleaseRequested != requested {
		return "", "", errors.New("public release snapshot unavailable")
	}
	switch snapshot.ReleaseVerdict {
	case "gone":
		return "", "", ErrReleaseGone
	case "conflict":
		return "", "", ErrReleaseConflict
	default:
		return snapshot.ReleaseID, snapshot.ReleaseDeploymentID, nil
	}
}

func (h *Handler) resolvePublicRevisionPin(ctx context.Context, app, scope, deployment string) (bool, error) {
	if h.publicRoutingPolicy == nil {
		return h.backend.(revisionPinResolver).ResolveRevisionPin(ctx, app, scope, deployment)
	}
	snapshot, ok := publicRoutingSnapshot(ctx)
	if !ok || snapshot.AppID != app || snapshot.Scope != scope || !snapshot.RevisionChecked || snapshot.RevisionID != deployment {
		return false, errors.New("public revision snapshot unavailable")
	}
	return snapshot.RevisionAllowed, nil
}
