package neon

import (
	"context"
	"net/http"
	"net/url"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

// The restore history limit (ADR-20261123062111 D2) shortens a project's
// history_retention_seconds, Neon's point-in-time restore window, while its
// subject is over the limit, and gives the spec's window back afterwards.
// Neon drops history older than the window; raising it again keeps history
// from then on and never brings the dropped history back.

var _ managedpostgres.HistoryRetentionProvider = (*Provider)(nil)

type historyRetentionProject struct {
	ID                      string `json:"id"`
	HistoryRetentionSeconds *int64 `json:"history_retention_seconds"`
}

type historyRetentionPatch struct {
	Project struct {
		HistoryRetentionSeconds int64 `json:"history_retention_seconds"`
	} `json:"project"`
}

func (p *Provider) ObserveHistoryRetention(ctx context.Context, providerResourceID string) (managedpostgres.HistoryRetentionObservation, error) {
	ref, err := parseResourceRef(providerResourceID)
	if err != nil {
		return managedpostgres.HistoryRetentionObservation{}, err
	}
	seconds, err := p.readHistoryRetention(ctx, ref.projectID)
	if err != nil {
		return managedpostgres.HistoryRetentionObservation{}, err
	}
	return managedpostgres.HistoryRetentionObservation{Scope: ref.projectID, Seconds: seconds}, nil
}

// SetHistoryRetention writes the project's restore window and reads it back,
// because an accepted PATCH alone is not evidence. It never writes zero,
// which turns point-in-time restore off, nor more than the backend offers.
func (p *Provider) SetHistoryRetention(ctx context.Context, providerResourceID string, seconds int64) error {
	if seconds <= 0 || p.maxRestoreWindow > 0 && seconds > p.maxRestoreWindow {
		return managedpostgres.ErrInvalid
	}
	ref, err := parseResourceRef(providerResourceID)
	if err != nil {
		return err
	}
	var patch historyRetentionPatch
	patch.Project.HistoryRetentionSeconds = seconds
	if err := p.doJSON(ctx, http.MethodPatch, "/projects/"+url.PathEscape(ref.projectID), nil, patch, nil, http.StatusOK); err != nil {
		return err
	}
	confirmed, err := p.readHistoryRetention(ctx, ref.projectID)
	if err != nil {
		return err
	}
	if confirmed != seconds {
		return managedpostgres.ErrUnavailable
	}
	return nil
}

func (p *Provider) readHistoryRetention(ctx context.Context, projectID string) (int64, error) {
	var response struct {
		Project historyRetentionProject `json:"project"`
	}
	if err := p.doJSON(ctx, http.MethodGet, "/projects/"+url.PathEscape(projectID), nil, nil, &response, http.StatusOK); err != nil {
		return 0, err
	}
	if response.Project.ID != projectID || response.Project.HistoryRetentionSeconds == nil || *response.Project.HistoryRetentionSeconds < 0 {
		return 0, managedpostgres.ErrUnavailable
	}
	return *response.Project.HistoryRetentionSeconds, nil
}
