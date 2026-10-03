package state

import (
	"context"
	"time"
)

// ClaimPreviewTeardown fences refreshes before external cleanup begins. The
// observed state and lease must still match the janitor's sweep snapshot;
// otherwise a concurrent reopen wins and the janitor skips this row. A claim
// is retryable after a crash or cleanup failure, including after soft-delete.
func (s *PgStore) ClaimPreviewTeardown(ctx context.Context, observed App, now time.Time) (App, error) {
	var app App
	row := s.pool.QueryRow(ctx, `
		update apps set preview_pr_state = $4
		where id = $1
		  and preview_of_slug is not null
		  and (
		    preview_pr_state = $4
		    or (preview_pr_state = $2
		        and preview_expires_at is not distinct from $3::timestamptz
		        and (preview_pr_state = $5 or preview_expires_at < $6))
		  )
		returning `+appsSelectColumns, observed.ID, observed.PreviewPrState,
		observed.PreviewExpiresAt, PreviewPrStateTearingDown, PreviewPrStateStale, now.UTC())
	if err := scanAppInto(&app, row); err != nil {
		return App{}, mapErr(err)
	}
	return app, nil
}

func (m *MemStore) ClaimPreviewTeardown(_ context.Context, observed App, now time.Time) (App, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	app, ok := m.apps[observed.ID]
	if !ok || app.PreviewOfSlug == "" {
		return App{}, ErrNotFound
	}
	if app.PreviewPrState != PreviewPrStateTearingDown {
		if app.PreviewPrState != observed.PreviewPrState || !samePreviewExpiry(app.PreviewExpiresAt, observed.PreviewExpiresAt) ||
			(app.PreviewPrState != PreviewPrStateStale && (app.PreviewExpiresAt == nil || !app.PreviewExpiresAt.Before(now))) {
			return App{}, ErrNotFound
		}
		app.PreviewPrState = PreviewPrStateTearingDown
		m.apps[app.ID] = app
	}
	return app, nil
}

func samePreviewExpiry(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}
