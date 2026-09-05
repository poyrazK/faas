package neon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math"
	"net/http"
	"net/url"

	"golang.org/x/sync/errgroup"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

const maximumProjectSearchPages = 10

type endpointSettings struct {
	MinimumCU            float64 `json:"autoscaling_limit_min_cu"`
	MaximumCU            float64 `json:"autoscaling_limit_max_cu"`
	SuspendTimeoutSecond int64   `json:"suspend_timeout_seconds"`
}

type projectSettings struct {
	Quota struct {
		LogicalSizeBytes *int64 `json:"logical_size_bytes"`
	} `json:"quota"`
}

type project struct {
	ID                      string           `json:"id"`
	Name                    string           `json:"name"`
	RegionID                string           `json:"region_id"`
	PostgresMajor           int              `json:"pg_version"`
	HistoryRetentionSeconds int64            `json:"history_retention_seconds"`
	DefaultEndpointSettings endpointSettings `json:"default_endpoint_settings"`
	Settings                projectSettings  `json:"settings"`
}

type operation struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

type branch struct {
	ID           string `json:"id"`
	CurrentState string `json:"current_state"`
	Default      bool   `json:"default"`
}

type endpoint struct {
	ID                   string  `json:"id"`
	BranchID             string  `json:"branch_id"`
	Host                 string  `json:"host"`
	Type                 string  `json:"type"`
	CurrentState         string  `json:"current_state"`
	MinimumCU            float64 `json:"autoscaling_limit_min_cu"`
	MaximumCU            float64 `json:"autoscaling_limit_max_cu"`
	SuspendTimeoutSecond int64   `json:"suspend_timeout_seconds"`
}

type projectResponse struct {
	Project project `json:"project"`
}

type projectsResponse struct {
	Projects   []project `json:"projects"`
	Pagination struct {
		Cursor string `json:"cursor"`
	} `json:"pagination"`
}

type branchesResponse struct {
	Branches []branch `json:"branches"`
}

type endpointsResponse struct {
	Endpoints []endpoint `json:"endpoints"`
}

type operationsResponse struct {
	Operations []operation `json:"operations"`
	Pagination struct {
		Cursor string `json:"cursor"`
	} `json:"pagination"`
}

type createProjectRequest struct {
	Project struct {
		Name                    string           `json:"name"`
		OrganizationID          string           `json:"org_id"`
		RegionID                string           `json:"region_id"`
		PostgresMajor           int              `json:"pg_version"`
		StorePasswords          bool             `json:"store_passwords"`
		HistoryRetentionSeconds int64            `json:"history_retention_seconds"`
		DefaultEndpointSettings endpointSettings `json:"default_endpoint_settings"`
		Settings                projectSettings  `json:"settings"`
		Branch                  struct {
			Name         string `json:"name"`
			RoleName     string `json:"role_name"`
			DatabaseName string `json:"database_name"`
		} `json:"branch"`
	} `json:"project"`
}

type createdProjectResponse struct {
	Project    project     `json:"project"`
	Operations []operation `json:"operations"`
}

func (p *Provider) Capabilities() managedpostgres.Capabilities {
	return managedpostgres.Capabilities{
		PostgresMajors:          []int{14, 15, 16, 17, 18},
		ServiceClasses:          []managedpostgres.ServiceClass{managedpostgres.ClassDevelopment, managedpostgres.ClassBurstable, managedpostgres.ClassProduction},
		Availability:            []managedpostgres.Availability{managedpostgres.AvailabilitySingleZone},
		ScaleToZero:             true,
		PooledConnections:       true,
		PointInTimeRestore:      p.maxRestoreWindow > 0,
		MaxRestoreWindowSeconds: p.maxRestoreWindow,
		MaxStorageBytes:         p.maxStorageBytes,
		UsageMeters:             []managedpostgres.Meter{managedpostgres.MeterComputeUnitSeconds, managedpostgres.MeterEgressBytes},
	}
}

func (p *Provider) Provision(ctx context.Context, request managedpostgres.ProvisionRequest) (managedpostgres.ObservedDatabase, error) {
	if request.ResourceID == "" || len(request.ResourceID) > 255 || request.IdempotencyKey == "" || len(request.IdempotencyKey) > 255 {
		return managedpostgres.ObservedDatabase{}, managedpostgres.ErrInvalid
	}
	if err := p.Capabilities().Supports(request.Spec); err != nil {
		return managedpostgres.ObservedDatabase{}, err
	}
	name := p.projectName(request.ResourceID)
	existingID, err := p.findProject(ctx, name)
	if err != nil {
		return managedpostgres.ObservedDatabase{}, err
	}
	if existingID != "" {
		return p.Inspect(ctx, existingID)
	}

	payload := p.projectPayload(name, request.Spec)
	var created createdProjectResponse
	if err := p.doJSON(ctx, http.MethodPost, "/projects", nil, payload, &created, http.StatusCreated); err != nil {
		// A successful create can lose its response. While the provider
		// context still has budget, discover the deterministic name once so
		// the caller can persist the opaque ID immediately. There is no
		// second POST here.
		if errors.Is(err, managedpostgres.ErrUnavailable) && ctx.Err() == nil {
			recoveredID, recoveryErr := p.findProject(ctx, name)
			if recoveryErr != nil {
				return managedpostgres.ObservedDatabase{}, recoveryErr
			}
			if recoveredID != "" {
				return managedpostgres.ObservedDatabase{
					ProviderResourceID: recoveredID,
					Status:             managedpostgres.ProviderStatusPending,
					Spec:               request.Spec,
				}, nil
			}
		}
		return managedpostgres.ObservedDatabase{}, err
	}
	if !validProviderID.MatchString(created.Project.ID) || created.Project.Name != name {
		return managedpostgres.ObservedDatabase{}, managedpostgres.ErrUnavailable
	}
	// Always persist the accepted upstream ID before polling. A later
	// reconcile uses Inspect and never repeats Neon's non-idempotent POST.
	return managedpostgres.ObservedDatabase{
		ProviderResourceID: created.Project.ID,
		Status:             managedpostgres.ProviderStatusPending,
		Spec:               request.Spec,
	}, nil
}

func (p *Provider) Inspect(ctx context.Context, providerResourceID string) (managedpostgres.ObservedDatabase, error) {
	if !validProviderID.MatchString(providerResourceID) {
		return managedpostgres.ObservedDatabase{}, managedpostgres.ErrInvalid
	}
	projectPath := "/projects/" + url.PathEscape(providerResourceID)
	var projectResult projectResponse
	var branchesResult branchesResponse
	var endpointsResult endpointsResponse
	var operationsResult operationsResponse
	group, groupContext := errgroup.WithContext(ctx)
	group.Go(func() error {
		return p.doJSON(groupContext, http.MethodGet, projectPath, nil, nil, &projectResult, http.StatusOK)
	})
	group.Go(func() error {
		return p.doJSON(groupContext, http.MethodGet, projectPath+"/branches", nil, nil, &branchesResult, http.StatusOK)
	})
	group.Go(func() error {
		return p.doJSON(groupContext, http.MethodGet, projectPath+"/endpoints", nil, nil, &endpointsResult, http.StatusOK)
	})
	group.Go(func() error {
		query := url.Values{"limit": {"1000"}}
		return p.doJSON(groupContext, http.MethodGet, projectPath+"/operations", query, nil, &operationsResult, http.StatusOK)
	})
	if err := group.Wait(); err != nil {
		return managedpostgres.ObservedDatabase{}, err
	}
	if projectResult.Project.ID != providerResourceID || operationsResult.Pagination.Cursor != "" {
		return managedpostgres.ObservedDatabase{}, managedpostgres.ErrUnavailable
	}
	defaultBranch, primaryEndpoint, resourcesReady := selectPrimary(branchesResult.Branches, endpointsResult.Endpoints)
	status := operationStatus(operationsResult.Operations, resourcesReady)
	observedSpec := p.observedSpec(projectResult.Project, primaryEndpoint)
	if defaultBranch.ID == "" {
		status = managedpostgres.ProviderStatusPending
	}
	return managedpostgres.ObservedDatabase{ProviderResourceID: providerResourceID, Status: status, Spec: observedSpec}, nil
}

func (*Provider) Update(context.Context, managedpostgres.UpdateRequest) (managedpostgres.ObservedDatabase, error) {
	// Neon applies class changes to endpoint resources rather than the
	// project defaults. Keep updates closed until the neutral service has a
	// durable multi-step update state machine and rollback semantics.
	return managedpostgres.ObservedDatabase{}, managedpostgres.ErrUnsupported
}

func (p *Provider) Delete(ctx context.Context, request managedpostgres.DeleteRequest) (managedpostgres.DeleteResult, error) {
	if request.IdempotencyKey == "" || (request.ProviderResourceID == "" && request.ResourceID == "") {
		return managedpostgres.DeleteResult{}, managedpostgres.ErrInvalid
	}
	providerResourceID := request.ProviderResourceID
	if providerResourceID == "" {
		if len(request.ResourceID) > 255 {
			return managedpostgres.DeleteResult{}, managedpostgres.ErrInvalid
		}
		var err error
		providerResourceID, err = p.findProject(ctx, p.projectName(request.ResourceID))
		if err != nil {
			return managedpostgres.DeleteResult{}, err
		}
		if providerResourceID == "" {
			return managedpostgres.DeleteResult{Done: true}, nil
		}
	}
	if !validProviderID.MatchString(providerResourceID) {
		return managedpostgres.DeleteResult{}, managedpostgres.ErrInvalid
	}
	path := "/projects/" + url.PathEscape(providerResourceID)
	var response projectResponse
	err := p.doJSON(ctx, http.MethodDelete, path, nil, nil, &response, http.StatusOK)
	if errors.Is(err, managedpostgres.ErrNotFound) {
		return managedpostgres.DeleteResult{Done: true}, nil
	}
	if err != nil {
		return managedpostgres.DeleteResult{}, err
	}
	return managedpostgres.DeleteResult{Done: true}, nil
}

func (p *Provider) projectPayload(name string, spec managedpostgres.Spec) createProjectRequest {
	profile := profiles[spec.Class]
	suspendTimeout := int64(-1)
	if spec.ScaleToZero {
		suspendTimeout = 300
	}
	var request createProjectRequest
	request.Project.Name = name
	request.Project.OrganizationID = p.organizationID
	request.Project.RegionID = p.regionID
	request.Project.PostgresMajor = spec.PostgresMajor
	request.Project.StorePasswords = true
	request.Project.HistoryRetentionSeconds = spec.RestoreWindowSeconds
	request.Project.DefaultEndpointSettings = endpointSettings{MinimumCU: profile.minimumCU, MaximumCU: profile.maximumCU, SuspendTimeoutSecond: suspendTimeout}
	request.Project.Settings.Quota.LogicalSizeBytes = &spec.StorageLimitBytes
	request.Project.Branch.Name = "production"
	request.Project.Branch.RoleName = "gregale_owner"
	request.Project.Branch.DatabaseName = p.databaseName
	return request
}

func (p *Provider) projectName(resourceID string) string {
	sum := sha256.Sum256([]byte(p.organizationID + "\x00" + resourceID))
	return "gregale-" + hex.EncodeToString(sum[:20])
}

func (p *Provider) findProject(ctx context.Context, name string) (string, error) {
	cursor := ""
	match := ""
	for page := 0; page < maximumProjectSearchPages; page++ {
		query := url.Values{"limit": {"400"}, "search": {name}, "org_id": {p.organizationID}}
		if cursor != "" {
			query.Set("cursor", cursor)
		}
		var response projectsResponse
		if err := p.doJSON(ctx, http.MethodGet, "/projects", query, nil, &response, http.StatusOK); err != nil {
			return "", err
		}
		for _, candidate := range response.Projects {
			if candidate.Name != name {
				continue
			}
			if match != "" || !validProviderID.MatchString(candidate.ID) {
				return "", managedpostgres.ErrConflict
			}
			match = candidate.ID
		}
		if response.Pagination.Cursor == "" {
			return match, nil
		}
		if response.Pagination.Cursor == cursor {
			return "", managedpostgres.ErrUnavailable
		}
		cursor = response.Pagination.Cursor
	}
	return "", managedpostgres.ErrUnavailable
}

func selectPrimary(branches []branch, endpoints []endpoint) (branch, endpoint, bool) {
	var primaryBranch branch
	for _, candidate := range branches {
		if !candidate.Default {
			continue
		}
		if primaryBranch.ID != "" {
			return branch{}, endpoint{}, false
		}
		primaryBranch = candidate
	}
	var primaryEndpoint endpoint
	for _, candidate := range endpoints {
		if candidate.BranchID != primaryBranch.ID || candidate.Type != "read_write" {
			continue
		}
		if primaryEndpoint.ID != "" {
			return primaryBranch, endpoint{}, false
		}
		primaryEndpoint = candidate
	}
	branchReady := primaryBranch.CurrentState == "ready"
	endpointReady := primaryEndpoint.CurrentState == "active" || primaryEndpoint.CurrentState == "idle"
	return primaryBranch, primaryEndpoint, branchReady && endpointReady
}

func operationStatus(operations []operation, resourcesReady bool) managedpostgres.ProviderStatus {
	pending := false
	terminalFailure := false
	for _, candidate := range operations {
		switch candidate.Status {
		case "finished", "skipped":
		case "error", "cancelled":
			terminalFailure = true
		default:
			pending = true
		}
	}
	if resourcesReady && !pending {
		return managedpostgres.ProviderStatusReady
	}
	if terminalFailure && !resourcesReady && !pending {
		return managedpostgres.ProviderStatusFailed
	}
	return managedpostgres.ProviderStatusPending
}

func (p *Provider) observedSpec(project project, primary endpoint) managedpostgres.Spec {
	storageLimit := int64(-1)
	if project.Settings.Quota.LogicalSizeBytes != nil {
		storageLimit = *project.Settings.Quota.LogicalSizeBytes
	}
	observed := managedpostgres.Spec{
		Region:               p.logicalRegion,
		PostgresMajor:        project.PostgresMajor,
		Availability:         managedpostgres.AvailabilitySingleZone,
		ScaleToZero:          primary.SuspendTimeoutSecond != -1,
		StorageLimitBytes:    storageLimit,
		RestoreWindowSeconds: project.HistoryRetentionSeconds,
	}
	if project.RegionID != p.regionID {
		observed.Region = ""
	}
	for class, profile := range profiles {
		if closeFloat(primary.MinimumCU, profile.minimumCU) && closeFloat(primary.MaximumCU, profile.maximumCU) {
			observed.Class = class
			break
		}
	}
	return observed
}

func closeFloat(left, right float64) bool {
	return math.Abs(left-right) < 0.000001
}
