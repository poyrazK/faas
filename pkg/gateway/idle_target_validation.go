// adr: 570
package gateway

import "net/http"

func (h *Handler) validateIdleTargetPick(r *http.Request, app App, pick PickResult, deployment, preferred, version string) (PickResult, error) {
	validator, ok := h.backend.(liveTargetValidator)
	if !ok {
		return pick, nil
	}
	live, err := validator.ValidateLiveTarget(r.Context(), app.ID, pick.Target.InstanceID)
	if err != nil {
		return PickResult{}, err
	}
	var current PickResult
	if deployment != "" {
		current = pickPublicDeployment(h.backend, app.ID, deployment, preferred)
	} else {
		current = h.pickForRequest(app, preferred, version)
	}
	if !live && current.OK && sameTargetPlacement(pick.Target, current.Target) {
		return PickResult{}, nil
	}
	return current, nil
}
