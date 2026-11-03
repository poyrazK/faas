package state

import (
	"context"
	"fmt"
	"maps"
	"slices"
)

// OrphanedPRPreviewMarker marks PR previews whose production app is gone for
// the preview janitor (ADR-20261028100000).
type OrphanedPRPreviewMarker interface {
	MarkOrphanedPRPreviewsForTeardown(ctx context.Context, limit int) (int, error)
}

// MarkOrphanedPRPreviewsForTeardown marks up to limit live PR previews whose
// production app is gone as tearing_down; the preview janitor tears them down
// on its next pass. A preview's production app is the parent of its PR
// environment's root, because a service that exists only in the PR has no
// production app of its own; a preview outside a recorded environment uses
// its own parent. A parent in its delete grace period counts as gone.
func (s *PgStore) MarkOrphanedPRPreviewsForTeardown(ctx context.Context, limit int) (int, error) {
	if limit < 1 {
		return 0, nil
	}
	tag, err := s.pool.Exec(ctx, `
		update apps set preview_pr_state = $1
		 where id in (
		   select p.id
		     from apps p
		     left join lateral (
		       select root.preview_of_slug as anchor
		         from pr_preview_sets s
		         join apps root on root.id = s.root_app_id
		        where s.root_app_id = p.id or p.id::text = any(s.member_app_ids)
		        order by s.updated_at desc
		        limit 1
		     ) env on true
		    where p.preview_of_slug is not null
		      and coalesce(p.preview_pr_number, 0) > 0
		      and p.status <> 'deleted'
		      and coalesce(p.preview_pr_state, '') not in ($1, $2)
		      and not exists (
		        select 1 from apps parent
		         where parent.account_id = p.account_id
		           and parent.slug = coalesce(env.anchor, p.preview_of_slug)
		           and parent.preview_of_slug is null
		           and parent.status <> 'deleted')
		    order by p.id
		    limit $3
		    for update of p skip locked)`,
		PreviewPrStateTearingDown, PreviewPrStateTornDown, limit)
	if err != nil {
		return 0, fmt.Errorf("state: mark orphaned PR previews: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

func (m *MemStore) MarkOrphanedPRPreviewsForTeardown(_ context.Context, limit int) (int, error) {
	if limit < 1 {
		return 0, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	anchors := make(map[string]string)
	for _, set := range m.previewSets {
		root, ok := m.apps[set.RootAppID]
		if !ok {
			continue
		}
		for _, id := range append([]string{set.RootAppID}, set.MemberAppIDs...) {
			anchors[id] = root.PreviewOfSlug
		}
	}
	marked := 0
	for _, id := range slices.Sorted(maps.Keys(m.apps)) {
		if marked == limit {
			break
		}
		app := m.apps[id]
		if app.PreviewOfSlug == "" || app.PreviewPrNumber <= 0 || app.Status == AppDeleted ||
			app.PreviewPrState == PreviewPrStateTearingDown || app.PreviewPrState == PreviewPrStateTornDown {
			continue
		}
		parent := app.PreviewOfSlug
		if anchor, ok := anchors[id]; ok {
			parent = anchor
		}
		if m.liveProductionAppLocked(app.AccountID, parent) {
			continue
		}
		app.PreviewPrState = PreviewPrStateTearingDown
		m.apps[id] = app
		marked++
	}
	return marked, nil
}

func (m *MemStore) liveProductionAppLocked(accountID, slug string) bool {
	for _, app := range m.apps {
		if app.AccountID == accountID && app.Slug == slug && app.PreviewOfSlug == "" && app.Status != AppDeleted {
			return true
		}
	}
	return false
}
