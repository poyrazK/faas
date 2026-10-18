package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/productcontrol"
)

// The live customer middleware supplies authority; none of these fields come
// from a customer body. Owner reads use the separate owner methods below.
type ProductHelpScope struct {
	ProductPlanScope
}

type ProductHelpStore interface {
	CreateProductHelp(context.Context, ProductHelpScope, api.ProductHelpCreate) (api.ProductHelpRequest, error)
	GetProductHelp(context.Context, ProductHelpScope, string) (api.ProductHelpRequest, error)
	ListProductHelp(context.Context, ProductHelpScope, string, string) (api.ProductHelpPage, error)
	GetProductOwnerHelp(context.Context, string, string, string) (api.ProductHelpRequest, error)
	ListProductOwnerHelp(context.Context, string, string, string, string, string) (api.ProductHelpPage, error)
	UpdateProductHelp(context.Context, string, string, string, api.ProductHelpUpdate) (api.ProductHelpRequest, error)
}

type productHelpView struct {
	head, current api.ProductCatalog
	customer      api.ProductCustomer
	permissions   api.ProductPermissions
}

type productHelpRow struct {
	authApp, org, fingerprint string
	value                     api.ProductHelpRequest
}

func helpAuthorized(scope ProductHelpScope, v productHelpView, tenant PlatformTenant) error {
	if tenant.Status != PlatformTenantActive || tenant.AccountID != scope.AccountID || v.customer.AuthOrganizationID != scope.OrganizationID || v.head.Spec.AuthApp != scope.AuthApp || v.permissions == (api.ProductPermissions{}) {
		return ErrProductAccess
	}
	return nil
}

func helpRowVisible(scope ProductHelpScope, row productHelpRow, p api.ProductPermissions) bool {
	r := row.value
	return r.AccountID == scope.AccountID && r.AppID == scope.AppID && r.TenantID == scope.TenantID && row.authApp == scope.AuthAppID && row.org == scope.OrganizationID && productcontrol.HelpVisible(r.Request.Kind, p)
}

func helpRequestValid(req api.ProductHelpCreate) error {
	if productcontrol.ValidateHelpCreate(req) != nil {
		return ErrInvalidArgument
	}
	return nil
}

func helpFilter(status string) bool {
	return status == "all" || status == "active" || productcontrol.HelpStatus(status)
}

func helpMatches(status, filter string) bool {
	return filter == "all" || status == filter || filter == "active" && status != "resolved"
}

func helpCursor(account, app, tenant, status, realm, raw string) (productOwnerCursor, error) {
	if !helpFilter(status) || tenant != "" && !validProductUUID(tenant) {
		return productOwnerCursor{}, ErrInvalidArgument
	}
	return decodeProductOwnerCursor(raw, productOwnerCursorScope("help/"+status, account, app, tenant, realm), true)
}

func helpContext(v productHelpView, now time.Time) api.ProductHelpContext {
	c := api.ProductHelpContext{ObservedAt: now.UTC(), Revision: v.customer.Revision, PolicyRevision: v.head.Revision, Period: productcontrol.Period(now), Plan: v.customer.Plan, Suspended: v.customer.Suspended, Reason: "workspace_reported"}
	if c.Suspended {
		c.Reason = "access_paused"
	}
	return c
}

func helpFeatureContext(v productHelpView, req api.ProductHelpCreate, starts int64, available bool, now time.Time) (api.ProductHelpContext, error) {
	if _, ok := v.current.Spec.Plans[v.customer.Plan].Features[req.Feature]; !ok {
		return api.ProductHelpContext{}, ErrNotFound
	}
	usage := api.ProductUsage{Period: productcontrol.Period(now), Lines: []api.ProductUsageLine{{Feature: req.Feature, Starts: starts}}}
	g, err := productcontrol.UsageGuidance("", "", v.customer, v.current, v.head, v.permissions, &usage, map[string]api.ProductResolvedBinding{req.Feature: {Available: available}}, now)
	if err != nil {
		return api.ProductHelpContext{}, err
	}
	c := helpContext(v, now)
	for i := range g.Features {
		if g.Features[i].Feature == req.Feature {
			c.Feature, c.Reason, c.Usage, c.ResetsAt = req.Feature, g.Features[i].State, &g.Features[i], &g.ResetsAt
			return c, nil
		}
	}
	return c, ErrNotFound
}

func helpNewRow(scope ProductHelpScope, req api.ProductHelpCreate, c api.ProductHelpContext, now time.Time) productHelpRow {
	now = now.UTC().Truncate(time.Microsecond)
	return productHelpRow{authApp: scope.AuthAppID, org: scope.OrganizationID, fingerprint: productOwnerHash(req), value: api.ProductHelpRequest{AccountID: scope.AccountID, AppID: scope.AppID, TenantID: scope.TenantID, Request: req, Context: c, Status: "open", Version: 1, CreatedAt: now, UpdatedAt: now}}
}

func helpCapacity(total, active int) error {
	if total >= api.ProductControlHelpMaxRequests {
		return NewOperationLimitError("product_help_requests", api.ProductControlHelpMaxRequests, int64(total+1))
	}
	if active >= api.ProductControlHelpMaxOpen {
		return NewOperationLimitError("product_help_open_requests", api.ProductControlHelpMaxOpen, int64(active+1))
	}
	return nil
}

func helpPage(rows []api.ProductHelpRequest, cursor productOwnerCursor) api.ProductHelpPage {
	page := api.ProductHelpPage{Requests: rows, ObservedAt: time.Now().UTC()}
	if len(rows) > api.ProductControlHelpPageSize {
		page.Requests = rows[:api.ProductControlHelpPageSize]
		last := page.Requests[len(page.Requests)-1]
		cursor.At, cursor.ID = last.CreatedAt, last.Request.ID
		page.NextCursor = encodeProductOwnerCursor(cursor)
	}
	return page
}

func helpUpdated(current api.ProductHelpRequest, u api.ProductHelpUpdate, now time.Time) (api.ProductHelpRequest, error) {
	if productcontrol.ValidateHelpUpdate(u) != nil {
		return current, ErrInvalidArgument
	}
	if current.Status == u.Status && current.Response == u.Response {
		return current, nil // Exact response-loss retry observes; it never reapplies.
	}
	if current.Version != u.ExpectedVersion || current.Version >= api.ProductControlHelpMaxUpdates {
		return current, ErrConflict
	}
	now = now.UTC().Truncate(time.Microsecond)
	if now.Before(current.UpdatedAt) {
		now = current.UpdatedAt
	}
	current.Status, current.Response, current.Version, current.UpdatedAt = u.Status, u.Response, current.Version+1, now
	return current, nil
}
