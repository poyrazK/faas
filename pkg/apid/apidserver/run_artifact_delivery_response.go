package apidserver

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/object"
	"github.com/onebox-faas/faas/pkg/apid/runartifacts"
	"github.com/onebox-faas/faas/pkg/state"
)

var errRunArtifactResponseWritten = errors.New("artifact destination authorization already wrote a response")

func (s *server) writeRunArtifactDelivery(w http.ResponseWriter, out api.RunArtifactDelivery, err error) {
	if errors.Is(err, errRunArtifactResponseWritten) {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if err != nil {
		var problem *api.Problem
		if errors.As(err, &problem) || errors.Is(err, state.ErrNotFound) || errors.Is(err, state.ErrConflict) {
			api.WriteProblem(w, runartifacts.Problem(err))
		} else {
			object.BucketProblem(w, err)
		}
		return
	}
	status := http.StatusOK
	if out.Status == "pending" {
		status = http.StatusAccepted
		w.Header().Set("Retry-After", strconv.Itoa(int(api.ObjectUploadRecoveryRetry.Seconds())))
	}
	writeJSON(w, status, out)
}
