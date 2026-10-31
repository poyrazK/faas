// Package resourcelabels is apid's side of resource labels
// (ADR-20261031100025): it turns label and selector errors into problems,
// reads ?selector=, and filters a list by it. Validation and matching live in
// pkg/labels; the rows live in the store.
package resourcelabels

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/labels"
	"github.com/onebox-faas/faas/pkg/state"
)

// Capability is the catalog id that owns labels, the label routes and
// ?selector= (pkg/productcap/catalog.json).
const Capability = "resource-labels"

// SelectorParam is the list query parameter a selector arrives in.
const SelectorParam = "selector"

const docsURL = "https://gregale.dev/docs/apps#labels"

// Problem turns a *labels.Error into a 400 that keeps its stable code and
// names the key or selector requirement at fault. Any other error is not a
// caller's mistake and gets nil.
func Problem(err error) *api.Problem {
	var le *labels.Error
	if !errors.As(err, &le) {
		return nil
	}
	title := "Invalid labels"
	if le.Code == labels.CodeInvalidSelector {
		title = "Invalid selector"
	}
	p := api.NewProblem(http.StatusBadRequest, le.Code, title, le.Error()).WithDocs(docsURL)
	if le.Limit > 0 {
		p = p.WithLimit(int64(le.Limit), int64(le.Observed))
	}
	return p
}

// Requested reports whether a list request carries a selector.
func Requested(q url.Values) bool {
	return strings.TrimSpace(q.Get(SelectorParam)) != ""
}

// ParseSelector reads ?selector=; an absent or blank one selects everything.
func ParseSelector(q url.Values) (labels.Selector, *api.Problem) {
	sel, err := labels.Parse(q.Get(SelectorParam))
	if err != nil {
		return labels.Selector{}, Problem(err)
	}
	return sel, nil
}

// NotReleased is the refusal for a selector while the capability is off for
// the caller. A plain list stays available.
func NotReleased(capabilityName string) *api.Problem {
	return api.ErrCapabilityNotReleased("?"+SelectorParam+"=", capabilityName)
}

// AppLabels reads the labels of apps, keyed by app ID; an app without labels
// is absent.
func AppLabels(ctx context.Context, store state.AppLabelStore, apps []state.App) (map[string]map[string]string, error) {
	ids := make([]string, len(apps))
	for i, a := range apps {
		ids[i] = a.ID
	}
	out, err := store.AppLabelsByID(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("read app labels: %w", err)
	}
	return out, nil
}

// SelectApps keeps the apps whose labels match sel, in order.
func SelectApps(apps []state.App, byApp map[string]map[string]string, sel labels.Selector) []state.App {
	if sel.Empty() {
		return apps
	}
	out := make([]state.App, 0, len(apps))
	for _, a := range apps {
		if sel.Matches(byApp[a.ID]) {
			out = append(out, a)
		}
	}
	return out
}
