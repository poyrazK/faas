package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func promotionReleaseInputsDB(ctx context.Context, db sqlc.DBTX, p ProjectEnvironmentPromotion) (promotionReleaseConfiguration, error) {
	raw, err := sqlc.New().ReadPromotionReleaseConfigurationInputs(ctx, db, sqlc.ReadPromotionReleaseConfigurationInputsParams{
		AccountID: p.AccountID, ProjectID: p.ProjectID, SourceScope: p.FromEnvironment, TargetScope: p.ToEnvironment,
		SourceReleaseID: p.SourceReleaseSetID, TargetReleaseID: p.PreviousTargetReleaseSetID})
	if err != nil {
		return promotionReleaseConfiguration{}, mapErr(err)
	}
	var capture promotionReleaseConfiguration
	if json.Unmarshal(raw, &capture) != nil {
		return capture, ErrConflict
	}
	for i := range capture.Workloads {
		normalizePromotionReleaseInputs(&capture.Workloads[i])
	}
	if p.Workers {
		for i := range capture.Workloads {
			w := &capture.Workloads[i]
			if w.SourceSettings.WorkloadClass == WorkloadClassWorker && w.SourceSnapshot == nil {
				ready, err := releaseWorkerReadyDB(ctx, db, p.AccountID, w.AppID, w.Source.ID)
				if err != nil {
					return capture, err
				}
				w.SourceWorkerQualified = ready != ""
			}
		}
		workerGraph := capture.TargetReleaseID
		if p.TargetReleaseSetID != "" {
			workerGraph = p.TargetReleaseSetID
		}
		if p.RestoredTargetReleaseSetID != "" {
			workerGraph = p.RestoredTargetReleaseSetID
		}
		rows, err := sqlc.New().ReadProjectReleaseWorkerCapture(ctx, db, sqlc.ReadProjectReleaseWorkerCaptureParams{AccountID: p.AccountID, ProjectID: p.ProjectID, BaselineID: workerGraph})
		if err != nil {
			return capture, err
		}
		var workers []*api.ProjectReleaseWorker
		for _, raw := range rows {
			var item projectWorkerCapture
			if json.Unmarshal(raw, &item) != nil {
				return capture, ErrConflict
			}
			worker, err := item.policy()
			if err != nil {
				return capture, err
			}
			worker, err = promotionWorkerPolicy(capture, worker)
			if err != nil {
				return capture, err
			}
			previous, err := sqlc.New().ReadRuntimeAppValuesForDeployment(ctx, db, sqlc.ReadRuntimeAppValuesForDeploymentParams{AccountID: mustPgUUID(p.AccountID), AppID: mustPgUUID(worker.AppID), DeploymentID: mustPgUUID(worker.PreviousDeploymentID)})
			if err != nil {
				return capture, mapErr(err)
			}
			var settings ProjectEnvironmentWorkloadSettings
			if json.Unmarshal(previous.Settings, &settings) != nil {
				return capture, ErrConflict
			}
			if err = validatePromotionWorkerBaseline(settings, worker); err != nil {
				return capture, err
			}
			workers = append(workers, worker)
		}
		sort.Slice(workers, func(i, j int) bool { return workers[i].AppID < workers[j].AppID })
		capture.setWorkers(workers)
	}

	if len(p.ReleaseSecrets) > 0 {
		input := projectReleaseConfigCapture{ProjectID: p.ProjectID}
		if err := loadReleaseSecretVersionsDB(ctx, db, &input, p.AccountID, promotionSecretRequest(p.ReleaseSecrets)); err != nil {
			return capture, err
		}
		capture.SecretVersions = input.SecretVersions
	}
	return capture, nil
}

func readPromotionReleaseCaptureDB(ctx context.Context, db sqlc.DBTX, accountID, promotionID string) (*promotionReleaseConfiguration, error) {
	raw, err := sqlc.New().ReadPromotionReleaseConfiguration(ctx, db, sqlc.ReadPromotionReleaseConfigurationParams{AccountID: accountID, PromotionID: promotionID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, mapErr(err)
	}
	var capture promotionReleaseConfiguration
	if json.Unmarshal(raw, &capture) != nil {
		return nil, ErrConflict
	}
	if err := capture.authenticate(); err != nil {
		return nil, err
	}
	return &capture, nil
}

func promotionRuntimeDB(ctx context.Context, db sqlc.DBTX) promotionRuntimeReader {
	return func(accountID, appID, deploymentID string) (RuntimeAppValuesSnapshot, error) {
		return runtimeAppValuesDB(ctx, db, accountID, appID, deploymentID)
	}
}

func (s *PgStore) PreviewProjectEnvironmentPromotionReleaseConfiguration(ctx context.Context, p ProjectEnvironmentPromotion) (*ProjectEnvironmentPromotionReleaseConfiguration, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	c, err := promotionReleaseInputsDB(ctx, tx, p)
	if err != nil {
		return nil, err
	}
	view, err := c.prepare(p, promotionRuntimeDB(ctx, tx))
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return view, nil
}

func (s *PgStore) ProjectEnvironmentPromotionReleaseConfiguration(ctx context.Context, accountID, promotionID string) (*ProjectEnvironmentPromotionReleaseConfiguration, error) {
	c, err := readPromotionReleaseCaptureDB(ctx, s.pool, accountID, promotionID)
	if err != nil || c == nil {
		return nil, err
	}
	return c.public(), nil
}

func lockPromotionReleaseInputsDB(ctx context.Context, tx pgx.Tx, p ProjectEnvironmentPromotion) error {
	q := sqlc.New()
	if _, err := q.LockProjectEnvironmentCloneApps(ctx, tx, sqlc.LockProjectEnvironmentCloneAppsParams{AccountID: mustPgUUID(p.AccountID), ProjectID: mustPgUUID(p.ProjectID)}); err != nil {
		return mapErr(err)
	}
	_, err := q.LockPromotionReleaseConfigurationDeployments(ctx, tx, sqlc.LockPromotionReleaseConfigurationDeploymentsParams{
		AccountID: p.AccountID, ProjectID: p.ProjectID, SourceReleaseID: p.SourceReleaseSetID, TargetReleaseID: p.PreviousTargetReleaseSetID, PromotionID: p.ID})
	return mapErr(err)
}

func capturePromotionReleaseConfigurationDB(ctx context.Context, tx pgx.Tx, p ProjectEnvironmentPromotion, workloads []ProjectEnvironmentPromotionWorkload) error {
	if err := lockPromotionReleaseInputsDB(ctx, tx, p); err != nil {
		return err
	}
	c, err := promotionReleaseInputsDB(ctx, tx, p)
	if err != nil {
		return err
	}
	view, err := c.prepare(p, promotionRuntimeDB(ctx, tx))
	if err != nil {
		return err
	}
	if view == nil {
		if p.ReleaseConfigurationHash != "" {
			return ErrConflict
		}
		return nil
	}
	if err := validatePromotionWorkerAdmission(p, &c); err != nil {
		return err
	}
	if (c.DestinationConfig != nil || c.Secrets != nil) && p.HealthPolicy == nil {
		return ErrInvalidArgument
	}
	if view.Hash != p.ReleaseConfigurationHash || c.SourceConfig.Hash != p.SourceConfigHash || c.TargetConfig.Hash != p.PreviousTargetConfigHash {
		return ErrConflict
	}
	if err := c.validateWorkloads(workloads); err != nil {
		return err
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return ErrConflict
	}
	q := sqlc.New()
	if err := q.SavePromotionReleaseConfiguration(ctx, tx, sqlc.SavePromotionReleaseConfigurationParams{PromotionID: p.ID, Payload: raw}); err != nil {
		return mapErr(err)
	}
	for _, w := range c.Workloads {
		if c.Secrets != nil {
			if err := pinReleaseSecretsDB(ctx, tx, w.Previous.ID, *w.SecretSnapshot); err != nil {
				return err
			}
		}
		raw, err := json.Marshal(w.PreviousVariables)
		if err != nil {
			return ErrConflict
		}
		if err := q.PinDeploymentReleaseEnvironment(ctx, tx, sqlc.PinDeploymentReleaseEnvironmentParams{DeploymentID: w.Previous.ID, ValuesJson: raw}); err != nil {
			return mapErr(err)
		}
	}
	return nil
}

func guardPromotionReleaseConfigurationDB(ctx context.Context, tx pgx.Tx, p ProjectEnvironmentPromotion, members []ProjectReleaseMember, rollback, completed bool) error {
	c, err := readPromotionReleaseCaptureDB(ctx, tx, p.AccountID, p.ID)
	if err != nil || c == nil {
		return err
	}
	if err := lockPromotionReleaseInputsDB(ctx, tx, p); err != nil {
		return err
	}
	p.Workers = len(c.workers()) > 0
	current, err := promotionReleaseInputsDB(ctx, tx, p)
	if err != nil {
		return err
	}
	runtime := promotionRuntimeDB(ctx, tx)
	if rollback && !completed {
		h, err := readPromotionHealthDB(ctx, tx, p.AccountID, p.ID)
		if err != nil {
			return err
		}
		if err := guardPromotionWorkerReadyDB(ctx, tx, p, h, true); err != nil {
			return err
		}
	}
	if !rollback && !completed {
		p.ResolveServiceBindings = c.DestinationConfig != nil
		p.Migrate = c.Migration != nil
		p.ReleaseSecrets = c.RequestedSecrets
		current.SecretVersions = c.secretVersions()
		view, err := current.prepare(p, runtime)
		if err != nil || view == nil || view.Hash != c.Hash {
			return fmt.Errorf("promotion source or destination changed since capture: %w", ErrConflict)
		}
	} else if err := c.guardTarget(current, runtime); err != nil {
		return err
	}
	if len(members) != len(c.Workloads) {
		return ErrConflict
	}
	for _, w := range c.Workloads {
		expectedID := w.Previous.ID
		if !rollback {
			raw, err := sqlc.New().ReadPromotionReleaseCandidate(ctx, tx, sqlc.ReadPromotionReleaseCandidateParams{PromotionID: p.ID, AccountID: p.AccountID, AppID: w.AppID})
			if err != nil {
				return err
			}
			var stored struct {
				projectCloneArtifact
				Status                 DeploymentStatus `json:"status"`
				TrafficPercent         int              `json:"traffic_percent"`
				TrafficPercentExplicit bool             `json:"traffic_percent_explicit"`
			}
			if json.Unmarshal(raw, &stored) != nil {
				return ErrConflict
			}
			candidate := stored.deployment("", stored.Scope, ProjectEnvironmentWorkloadSettings{})
			candidate.ID, candidate.Status, candidate.TrafficPercent, candidate.TrafficPercentExplicit = stored.ID, stored.Status, stored.TrafficPercent, stored.TrafficPercentExplicit
			expectedID = candidate.ID
			if err := validatePromotionReleaseCandidate(*c, w, candidate, runtime); err != nil {
				return err
			}
		}
		if releaseMemberForApp(ProjectReleaseSet{Members: members}, w.AppID) != expectedID {
			return ErrConflict
		}
	}
	return nil
}
