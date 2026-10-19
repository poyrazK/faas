package datapipeline

import (
	"context"

	"github.com/google/uuid"
)

func (s *Service) advanceDataAPI(ctx context.Context, job *Setup) error {
	p, err := s.Store.Get(ctx, job.AccountID, job.PipelineID)
	if err != nil {
		return err
	}
	if p.DesiredState != "running" || p.State == "blocked" || p.State == "resync_required" || p.State == "deleted" {
		return &blocked{code: "pipeline_needs_attention"}
	}
	if p.State != "live" || !p.ObservationFresh {
		return nil
	}
	if s.PrepareDataAPI == nil || s.DeployDataAPI == nil || s.DataAPIState == nil {
		return ErrUnavailable
	}
	switch job.Phase {
	case "configuring_api":
		binding, ready, err := s.PrepareDataAPI(ctx, *job)
		if binding != "" {
			if job.DataAPIPlan.BindingID != "" && job.DataAPIPlan.BindingID != binding {
				return ErrConflict
			}
			job.DataAPIPlan.BindingID = binding
		}
		if err != nil {
			return err
		}
		if ready {
			job.Phase = "deploying_api"
		}
	case "deploying_api":
		deployment, build, err := s.DeployDataAPI(ctx, *job)
		if err != nil {
			return err
		}
		job.DataAPIPlan.DeploymentID, job.DataAPIPlan.BuildID = deployment, build
		job.Phase = "waiting_api"
	case "waiting_api":
		status, endpoint, err := s.DataAPIState(ctx, *job)
		if err != nil {
			return err
		}
		switch status {
		case "live":
			job.DataAPIPlan.Endpoint, job.State = endpoint, "ready"
		case "failed", "cancelled", "superseded":
			job.Phase = "api_failed"
			return &blocked{code: "data_api_deployment_failed"}
		}
	case "api_failed":
		// Reached only after an explicit retry. A terminal build needs a new
		// attempt; persist that identity before submission on the next tick.
		job.DataAPIPlan.PreviousDeploymentID = job.DataAPIPlan.DeploymentID
		job.DataAPIPlan.PreviousBuildID = job.DataAPIPlan.BuildID
		job.DataAPIPlan.DeploymentID, job.DataAPIPlan.BuildID = "", ""
		job.DataAPIPlan.IntentID = uuid.NewString()
		job.Phase = "deploying_api"
	}
	return nil
}
