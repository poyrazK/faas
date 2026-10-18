package appaccess

import (
	"context"
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/state"
)

// VisiblePage walks hidden pages internally. Its public cursor always names a
// visible row, so a departed member cannot recover company IDs/secret names
// from cursors, and older personal resources remain reachable.
func VisiblePage[T any](ctx context.Context, before string, limit int, fetch func(string) ([]T, error), filter func([]T) ([]T, error), cursor func(T) string) ([]T, string, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, "", err
		}
		rows, err := fetch(before)
		if err != nil {
			return nil, "", err
		}
		visible, err := filter(rows)
		if err != nil {
			return nil, "", err
		}
		if len(visible) > 0 {
			next := ""
			if len(rows) == limit {
				next = cursor(visible[len(visible)-1])
			}
			return visible, next, nil
		}
		if len(rows) < limit {
			return make([]T, 0), "", nil
		}
		next := cursor(rows[len(rows)-1])
		if next == "" || next == before {
			return nil, "", errors.New("application inventory cursor did not advance")
		}
		before = next
	}
}

func VisibleResources[T any](r *http.Request, store Store, actor state.Account, rows []T, appID func(T) string) ([]T, error) {
	apps := make([]state.App, 0)
	seen := make(map[string]bool)
	for _, row := range rows {
		id := appID(row)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		app, err := store.AppByID(r.Context(), id)
		if errors.Is(err, state.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		apps = append(apps, app)
	}
	visible, err := VisibleApps(r, store, actor, apps)
	if err != nil {
		return nil, err
	}
	allowed := map[string]bool{"": true} // Account-scoped rows have no app.
	for _, app := range visible {
		allowed[app.ID] = true
	}
	out := make([]T, 0, len(rows))
	for _, row := range rows {
		if allowed[appID(row)] {
			out = append(out, row)
		}
	}
	return out, nil
}
