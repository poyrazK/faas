package neon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

var _ managedpostgres.SnapshotCopyReaderProvider = (*Provider)(nil)

type snapshotCopyReaderCreateRequest struct {
	Endpoint snapshotCopyReaderCreateEndpoint `json:"endpoint"`
}

type snapshotCopyReaderCreateEndpoint struct {
	Name               string `json:"name"`
	BranchID           string `json:"branch_id"`
	RegionID           string `json:"region_id"`
	Type               string `json:"type"`
	Disabled           bool   `json:"disabled"`
	PasswordlessAccess bool   `json:"passwordless_access"`
	endpointSettings
}

func (p *Provider) PrepareSnapshotCopyReader(ctx context.Context, d managedpostgres.RestoreSourceDefinition, r managedpostgres.SnapshotCopyReaderRequest) (managedpostgres.SnapshotCopyReaderObservation, error) {
	return p.snapshotCopyReader(ctx, d, r, true)
}

func (p *Provider) FindSnapshotCopyReader(ctx context.Context, d managedpostgres.RestoreSourceDefinition, r managedpostgres.SnapshotCopyReaderRequest) (managedpostgres.SnapshotCopyReaderObservation, error) {
	return p.snapshotCopyReader(ctx, d, r, false)
}

func (p *Provider) snapshotCopyReader(ctx context.Context, d managedpostgres.RestoreSourceDefinition, r managedpostgres.SnapshotCopyReaderRequest, create bool) (managedpostgres.SnapshotCopyReaderObservation, error) {
	if p == nil {
		return managedpostgres.SnapshotCopyReaderObservation{}, managedpostgres.ErrUnavailable
	}
	if r.Validate() != nil || r.ExpectedEndpointID != "" && !validCopyReaderEndpointID(r.ExpectedEndpointID) {
		return managedpostgres.SnapshotCopyReaderObservation{}, managedpostgres.ErrInvalid
	}
	capture, err := p.authenticateSnapshotCopyReaderCapture(ctx, d, r, create)
	if err != nil {
		return managedpostgres.SnapshotCopyReaderObservation{}, err
	}
	name := p.snapshotCopyReaderName(r.ResourceID)
	actual, err := p.findSnapshotCopyReaderEndpoint(ctx, capture.projectID, r.ExpectedEndpointID, name)
	if errors.Is(err, managedpostgres.ErrNotFound) && create && r.ExpectedEndpointID == "" {
		var accepted struct {
			Endpoint endpoint `json:"endpoint"`
		}
		path := "/projects/" + url.PathEscape(capture.projectID) + "/endpoints"
		postErr := p.doJSON(ctx, http.MethodPost, path, nil, snapshotCopyReaderPayload(name, capture.branchID, p.regionID, d.Spec), &accepted, http.StatusCreated)
		if postErr == nil {
			if _, err := p.snapshotCopyReaderObservation(d.Spec, r, capture, accepted.Endpoint); err != nil {
				return managedpostgres.SnapshotCopyReaderObservation{}, err
			}
			actual, err = p.findSnapshotCopyReaderEndpoint(ctx, capture.projectID, accepted.Endpoint.ID, name)
			// A changed creation time between the acknowledgement and exact
			// read is replacement, even before the worker persists its pin.
			if err == nil && !sameCopyReaderCreation(actual.CreatedAt, accepted.Endpoint.CreatedAt) {
				err = managedpostgres.ErrConflict
			}
		} else if errors.Is(postErr, managedpostgres.ErrUnavailable) && ctx.Err() == nil {
			actual, err = p.findSnapshotCopyReaderEndpoint(ctx, capture.projectID, "", name)
			if errors.Is(err, managedpostgres.ErrNotFound) {
				err = managedpostgres.ErrUnavailable
			}
		} else {
			err = postErr
		}
	}
	if err != nil {
		return managedpostgres.SnapshotCopyReaderObservation{}, err
	}
	observed, err := p.snapshotCopyReaderObservation(d.Spec, r, capture, actual)
	if err != nil {
		return observed, err
	}
	if _, err := p.authenticateSnapshotCopyReaderCapture(ctx, d, r, create); err != nil {
		return observed, err
	}
	return observed, nil
}

func snapshotCopyReaderPayload(name, branchID, region string, spec managedpostgres.Spec) snapshotCopyReaderCreateRequest {
	return snapshotCopyReaderCreateRequest{Endpoint: snapshotCopyReaderCreateEndpoint{Name: name, BranchID: branchID, RegionID: region,
		Type: "read_only", Disabled: false, PasswordlessAccess: false, endpointSettings: endpointSettingsForSpec(spec)}}
}

func (p *Provider) authenticateSnapshotCopyReaderCapture(ctx context.Context, d managedpostgres.RestoreSourceDefinition, r managedpostgres.SnapshotCopyReaderRequest, retained bool) (resourceRef, error) {
	var actual managedpostgres.SnapshotRestoreObservation
	var err error
	if retained {
		actual, err = p.FindSnapshotRestore(ctx, d, r.Capture)
	} else {
		// Recovery uses the committed snapshot/fork pins. Disposing the
		// source snapshot during compensation must not hide an uncertain
		// owned endpoint on the independently authenticated native fork.
		actual, err = p.snapshotCopyReaderCaptureFromReceipt(ctx, d, r)
	}
	if err != nil {
		return resourceRef{}, err
	}
	if !actual.Restored || !actual.SnapshotCreatedAt.Equal(r.SnapshotCreatedAt) || !actual.TargetCreatedAt.Equal(r.CaptureCreatedAt) {
		return resourceRef{}, managedpostgres.ErrConflict
	}
	return parseResourceRef(actual.ProviderResourceID)
}

func (p *Provider) snapshotCopyReaderCaptureFromReceipt(ctx context.Context, d managedpostgres.RestoreSourceDefinition, r managedpostgres.SnapshotCopyReaderRequest) (managedpostgres.SnapshotRestoreObservation, error) {
	source, snapshotID, targetID, err := p.snapshotRestoreSelectors(d, r.Capture)
	if err != nil {
		return managedpostgres.SnapshotRestoreObservation{}, err
	}
	if err := p.snapshotRestorePlacement(ctx, source.projectID, d.Spec); err != nil {
		return managedpostgres.SnapshotRestoreObservation{}, err
	}
	branch, err := p.findSnapshotRestoreBranch(ctx, source.projectID, targetID, p.restoreBranchName(r.Capture.ResourceID))
	if err != nil {
		return managedpostgres.SnapshotRestoreObservation{}, err
	}
	pin := managedpostgres.DatabaseSnapshot{ProviderSnapshotID: r.Capture.ProviderSnapshotID, SourceResourceID: source.String(),
		PointInTime: r.Capture.Snapshot.PointInTime, CreatedAt: r.SnapshotCreatedAt}
	return snapshotRestoreObservation(source, snapshotID, p.restoreBranchName(r.Capture.ResourceID), pin, branch)
}

func (p *Provider) snapshotCopyReaderName(owner string) string {
	sum := sha256.Sum256([]byte(p.organizationID + "\x00snapshot-copy-reader\x00" + owner))
	return "gregale-reader-" + hex.EncodeToString(sum[:20])
}

func (p *Provider) findSnapshotCopyReaderEndpoint(ctx context.Context, projectID, id, name string) (endpoint, error) {
	path := "/projects/" + url.PathEscape(projectID) + "/endpoints"
	if id != "" {
		var response struct {
			Endpoint endpoint `json:"endpoint"`
		}
		if err := p.doJSON(ctx, http.MethodGet, path+"/"+url.PathEscape(id), nil, nil, &response, http.StatusOK); err != nil {
			return endpoint{}, err
		}
		if response.Endpoint.ID != id || response.Endpoint.Name != name {
			return endpoint{}, managedpostgres.ErrConflict
		}
		return response.Endpoint, nil
	}
	// The current API returns the complete project array without pagination.
	// Unknown pagination cannot establish absence or authorize a new POST.
	var response struct {
		Endpoints  []endpoint      `json:"endpoints"`
		Pagination json.RawMessage `json:"pagination"`
	}
	if err := p.doJSON(ctx, http.MethodGet, path, nil, nil, &response, http.StatusOK); err != nil {
		return endpoint{}, err
	}
	if response.Endpoints == nil || response.Pagination != nil {
		return endpoint{}, managedpostgres.ErrUnavailable
	}
	seen := map[string]bool{}
	var found endpoint
	for _, candidate := range response.Endpoints {
		if !validCopyReaderEndpointID(candidate.ID) || candidate.ProjectID != projectID || seen[candidate.ID] {
			return endpoint{}, managedpostgres.ErrConflict
		}
		seen[candidate.ID] = true
		if candidate.Name == name {
			if found.ID != "" {
				return endpoint{}, managedpostgres.ErrConflict
			}
			found = candidate
		}
	}
	if found.ID == "" {
		return endpoint{}, managedpostgres.ErrNotFound
	}
	// Pin list-observed creation time before the exact GET too.
	actual, err := p.findSnapshotCopyReaderEndpoint(ctx, projectID, found.ID, name)
	if err == nil && !sameCopyReaderCreation(actual.CreatedAt, found.CreatedAt) {
		err = managedpostgres.ErrConflict
	}
	return actual, err
}

func (p *Provider) snapshotCopyReaderObservation(spec managedpostgres.Spec, r managedpostgres.SnapshotCopyReaderRequest, capture resourceRef, actual endpoint) (managedpostgres.SnapshotCopyReaderObservation, error) {
	at, err := time.Parse(time.RFC3339Nano, actual.CreatedAt)
	if !validCopyReaderEndpointID(actual.ID) || actual.Name != p.snapshotCopyReaderName(r.ResourceID) || actual.ProjectID != capture.projectID ||
		actual.BranchID != capture.branchID || actual.RegionID != p.regionID || actual.Type != "read_only" ||
		err != nil || at.IsZero() || at.Before(r.RequestedAt) || at.After(time.Now()) || at.Nanosecond()%1000 != 0 ||
		r.ExpectedEndpointID != "" && (actual.ID != r.ExpectedEndpointID || !at.Equal(r.ExpectedCreatedAt)) {
		return managedpostgres.SnapshotCopyReaderObservation{}, managedpostgres.ErrConflict
	}
	settings := endpointSettingsForSpec(spec)
	if actual.MinimumCU != settings.MinimumCU || actual.MaximumCU != settings.MaximumCU || actual.SuspendTimeoutSecond != settings.SuspendTimeoutSecond {
		return managedpostgres.SnapshotCopyReaderObservation{}, managedpostgres.ErrConflict
	}
	available := actual.Disabled != nil && !*actual.Disabled && actual.PasswordlessAccess != nil && !*actual.PasswordlessAccess &&
		actual.Host != "" && strings.HasPrefix(actual.Host, actual.ID+".") && actual.PendingState == "" && (actual.CurrentState == "active" || actual.CurrentState == "idle")
	return managedpostgres.SnapshotCopyReaderObservation{EndpointID: actual.ID, CreatedAt: at.UTC(), Available: available}, nil
}

func validCopyReaderEndpointID(id string) bool {
	return validProviderID.MatchString(id) && strings.HasPrefix(id, "ep-")
}

func sameCopyReaderCreation(a, b string) bool {
	x, err := time.Parse(time.RFC3339Nano, a)
	y, otherErr := time.Parse(time.RFC3339Nano, b)
	return err == nil && otherErr == nil && !x.IsZero() && x.Equal(y)
}
