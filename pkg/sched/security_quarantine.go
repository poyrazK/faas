package sched

import (
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// securityQuarantineErr refuses to boot a deployment imaged quarantined after
// a regressed or expired security scan (ParkReasonSecurityScanRegressed). The
// gateway and apid refuse its traffic, but crons, service-mesh calls, floors,
// prewarm and app tasks reach schedd directly, so every boot path checks it
// here. The quarantine ends when a clean replacement becomes the live
// deployment (cmd/apid/handlers_security.go), which carries no parked reason.
func securityQuarantineErr(dep state.Deployment) error {
	if dep.ParkedReason != string(state.ParkReasonSecurityScanRegressed) {
		return nil
	}
	return errors.Join(ErrPermanentWake, api.NewProblem(http.StatusConflict, api.CodeSecurityPostureBlocked,
		"App is security quarantined",
		"the live deployment has blocking or unavailable image-scan evidence; deploy a remediated image to recover"))
}
