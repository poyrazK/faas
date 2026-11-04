package mcpmanagement

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

type resourcePageArgs struct {
	App         string `json:"app"`
	Project     string `json:"project,omitempty"`
	Environment string `json:"environment,omitempty"`
	Limit       int    `json:"limit,omitempty"`
	Offset      int    `json:"offset,omitempty"`
}

type cronChangeArgs struct {
	App      string  `json:"app"`
	CronID   string  `json:"cron_id"`
	Reason   string  `json:"reason"`
	Delete   bool    `json:"delete,omitempty"`
	Schedule *string `json:"schedule,omitempty"`
	Path     *string `json:"path,omitempty"`
	Timezone *string `json:"timezone,omitempty"`
	Enabled  *bool   `json:"enabled,omitempty"`
}

type domainRemovalArgs struct {
	App    string `json:"app"`
	Domain string `json:"domain"`
	Reason string `json:"reason"`
}

type appCostArgs struct {
	App   string `json:"app"`
	Month string `json:"month,omitempty" jsonschema:"UTC billing month YYYY-MM; default current"`
}

func registerResourceInventory(s *mcp.Server, m *management) {
	register(s, m, "gregale_list_domains", "Read one native page of the selected app's default-scope domain metadata. Requires apps:read; no challenge tokens.", m.listDomains)
	register(s, m, "gregale_list_crons", "Read one native page of the selected app's scheduled-request metadata. Requires apps:read; no command payloads.", m.listCrons)
	register(s, m, "gregale_list_buckets", "Read one native page of bucket metadata in the selected app and scope. Requires apps:read; grants no object or credential access.", m.listBuckets)
	register(s, m, "gregale_get_storage_bindings", "Read one native page of selected-scope bucket binding references and rotation state. Requires apps:read; no credential material.", m.getStorageBindings)
	register(s, m, "gregale_list_databases", "Read one native page of database and binding metadata in the selected app and scope. Requires apps:read; grants no connections or lifecycle access.", m.listDatabases)
	register(s, m, "gregale_plan_cron_change", "Review changing or deleting an exact cron owned by the selected app. Pins its current configuration; apply with gregale_apply_operation.", m.planCronChange)
	register(s, m, "gregale_plan_domain_removal", "Review detaching an exact custom domain from the selected app. Serving on that domain stops; DNS records at the provider remain. Apply with gregale_apply_operation.", m.planDomainRemoval)
	if m.hasCapability(api.ScopeUsageRead) {
		register(s, m, "gregale_get_costs", "Read costs attributed to the selected app for a UTC billing month. Native coverage and shared-cost limitations remain explicit.", m.getCosts)
	}
}

func (m *management) listDomains(ctx context.Context, a resourcePageArgs) (any, error) {
	return m.resourceInventory(ctx, a, "domains")
}
func (m *management) listCrons(ctx context.Context, a resourcePageArgs) (any, error) {
	return m.resourceInventory(ctx, a, "crons")
}
func (m *management) listBuckets(ctx context.Context, a resourcePageArgs) (any, error) {
	return m.resourceInventory(ctx, a, "buckets")
}
func (m *management) getStorageBindings(ctx context.Context, a resourcePageArgs) (any, error) {
	return m.resourceInventory(ctx, a, "storage_bindings")
}
func (m *management) listDatabases(ctx context.Context, a resourcePageArgs) (any, error) {
	return m.resourceInventory(ctx, a, "databases")
}

func (m *management) resourceInventory(ctx context.Context, a resourcePageArgs, kind string) (any, error) {
	if (kind == "domains" || kind == "crons") && (a.Project != "" || a.Environment != "") {
		return nil, invalid("unsupported_scope", "domain and cron inventory is app-wide and requires default-scope access")
	}
	limit, err := pageLimit(a.Limit)
	if err != nil {
		return nil, err
	}
	if a.Offset < 0 || a.Offset > api.AppResourceInventoryMaxOffset {
		return nil, invalid("invalid_argument", "offset is outside its allowed bounds")
	}
	app, env, err := m.appEnvironment(ctx, a.App, a.Project, a.Environment, false)
	if err != nil {
		return nil, err
	}
	if app.ID == "" || app.Slug != a.App || !m.appIDAllowed(app.ID) {
		return nil, invalid("app_access_denied", "app identity is outside consent")
	}
	out, err := m.client.GetAppResourceInventory(ctx, a.App, kind, a.Environment, limit, a.Offset)
	if err != nil {
		return nil, err
	}
	if !validResourcePage(out, app.ID, env.ProjectID, env.ID, normalizedScope(a.Environment), kind, limit, a.Offset) {
		return nil, invalid("invalid_response", "resource inventory identity or pagination differs from the selected app and scope")
	}
	return out, nil
}

func validResourcePage(p api.AppResourceInventoryResponse, appID, projectID, envID, scope, kind string, limit, offset int) bool {
	if p.SchemaVersion != 1 || p.AppID != appID || p.ProjectID != projectID || p.EnvironmentID != envID || p.Scope != scope || p.Kind != kind || p.Limit != limit || p.Offset != offset || p.Total < 0 || p.GeneratedAt.IsZero() || p.Items == nil || len(p.Items) > limit {
		return false
	}
	end := int64(offset) + int64(len(p.Items))
	if p.HasMore {
		if p.NextOffset == nil || *p.NextOffset != offset+limit || len(p.Items) != limit || p.Total <= end {
			return false
		}
	} else if p.NextOffset != nil || offset <= int(p.Total) && end != p.Total || offset > int(p.Total) && len(p.Items) != 0 {
		return false
	}
	seen := map[string]bool{}
	for _, item := range p.Items {
		if item.ID == "" || item.AppID != appID || item.Scope != scope {
			return false
		}
		key := item.ID
		switch kind {
		case "domains":
			if item.Domain != item.ID || item.Verified == nil || item.CertStatus == "" {
				return false
			}
		case "crons":
			if item.Enabled == nil || item.Schedule == "" || item.Timezone == "" {
				return false
			}
		case "buckets":
			if item.Name == "" || item.State == "" || item.Region == "" {
				return false
			}
		case "storage_bindings":
			if item.BucketID == "" || item.Bucket == "" || item.RotationPending == nil {
				return false
			}
		case "databases":
			b := item.Binding
			if item.Name == "" || item.Region == "" || item.State == "" || item.PostgresMajor <= 0 || b == nil || b.ID == "" || b.DatabaseID != item.ID || b.AppID != appID || b.Scope != scope || b.State == "" || b.Access == "" || b.EnvironmentKey == "" {
				return false
			}
			key += "/" + b.ID
		default:
			return false
		}
		if seen[key] {
			return false
		}
		seen[key] = true
	}
	return true
}

func (m *management) getCosts(ctx context.Context, a appCostArgs) (any, error) {
	if a.Month != "" {
		month, err := time.Parse("2006-01", a.Month)
		if err != nil || month.After(time.Now()) {
			return nil, invalid("invalid_argument", "month must be a current or historical YYYY-MM")
		}
	}
	app, _, err := m.appEnvironment(ctx, a.App, "", "", false)
	if err != nil {
		return nil, err
	}
	if app.ID == "" || app.Slug != a.App || !m.appIDAllowed(app.ID) {
		return nil, invalid("app_access_denied", "app identity is outside consent")
	}
	out, err := m.client.GetAppCosts(ctx, a.App, a.Month)
	if err != nil {
		return nil, err
	}
	if out.AppID != app.ID || out.AppSlug != a.App || out.Scope != "application_attributed_retained_compute_and_interface_egress" || out.BillEstimateAvailable {
		return nil, invalid("invalid_response", "cost report does not identify the selected application or its coverage")
	}
	for _, meter := range out.Meters {
		for _, allocation := range meter.Allocations {
			if allocation.Attribution.AppID != app.ID {
				return nil, invalid("invalid_response", "cost allocation identifies another application")
			}
		}
	}
	return out, nil
}

func (m *management) planCronChange(ctx context.Context, a cronChangeArgs) (any, error) {
	if err := validateSegment(a.CronID, "cron_id"); err != nil {
		return nil, err
	}
	if !a.Delete && a.Schedule == nil && a.Path == nil && a.Timezone == nil && a.Enabled == nil {
		return nil, invalid("invalid_argument", "provide a cron change or delete=true")
	}
	if a.Path != nil && (!strings.HasPrefix(*a.Path, "/") || strings.ContainsAny(*a.Path, " \r\n")) {
		return nil, invalid("invalid_argument", "cron path must begin with / and contain no whitespace")
	}
	kind := "cron_update"
	if a.Delete {
		kind = "cron_delete"
	}
	req := api.UpdateCronRequest{Schedule: a.Schedule, Path: a.Path, Timezone: a.Timezone, Enabled: a.Enabled}
	p, err := m.operationBase(ctx, operationArgs{App: a.App, Reason: a.Reason}, kind, req)
	if err != nil {
		return nil, err
	}
	cron, err := m.client.GetCron(ctx, a.CronID)
	if err != nil {
		return nil, err
	}
	if cron.AppID != p.AppID || cron.ID != a.CronID {
		return nil, invalid("app_access_denied", "cron does not belong to the selected app")
	}
	p.Operation.ResourceID, p.Operation.ResourceHash = a.CronID, cronFingerprint(cron)
	p.Operation.Changes = p.Operation.Request
	p.TrafficEffect = "Change the exact reviewed schedule. Deletion stops future scheduled requests; already running requests continue."
	return m.savePlan(p)
}

func (m *management) planDomainRemoval(ctx context.Context, a domainRemovalArgs) (any, error) {
	domain := strings.ToLower(strings.TrimSuffix(a.Domain, "."))
	if !domainName.MatchString(domain) {
		return nil, invalid("invalid_argument", "domain must be a hostname")
	}
	p, err := m.operationBase(ctx, operationArgs{App: a.App, Reason: a.Reason}, "domain_delete", struct{}{})
	if err != nil {
		return nil, err
	}
	d, err := m.client.GetDomain(ctx, domain)
	if err != nil {
		return nil, err
	}
	if d.AppID != p.AppID || normalizedScope(d.Environment) != p.Scope {
		return nil, invalid("app_access_denied", "domain does not belong to the selected app")
	}
	if d.Default {
		return nil, invalid("unsupported_resource", "the app's built-in default domain cannot be detached")
	}
	p.Operation.ResourceID, p.Operation.ResourceHash = domain, resourceFingerprint(map[string]string{"app_id": d.AppID, "domain": d.Domain})
	p.TrafficEffect = "Detach " + domain + "; requests to this custom domain stop serving. Provider DNS records remain."
	return m.savePlan(p)
}

func (m *management) applyResourceOperation(ctx context.Context, p *releasePlan) (any, error) {
	if p.Operation.Kind == "domain_delete" {
		d, err := m.client.GetDomain(ctx, p.Operation.ResourceID)
		if notFound(err) {
			return map[string]any{"deleted": true}, nil
		}
		if err != nil {
			return nil, err
		}
		if d.AppID != p.AppID || normalizedScope(d.Environment) != p.Scope || resourceFingerprint(map[string]string{"app_id": d.AppID, "domain": d.Domain}) != p.Operation.ResourceHash {
			return nil, invalid("plan_stale", "domain ownership changed")
		}
		err = m.client.DeleteDomain(ctx, p.Operation.ResourceID)
		return map[string]any{"deleted": err == nil}, err
	}
	c, err := m.client.GetCron(ctx, p.Operation.ResourceID)
	if notFound(err) && p.Operation.Kind == "cron_delete" {
		return map[string]any{"deleted": true}, nil
	}
	if err != nil {
		return nil, err
	}
	if c.ID != p.Operation.ResourceID || c.AppID != p.AppID || cronFingerprint(c) != p.Operation.ResourceHash {
		return nil, invalid("plan_stale", "cron changed since review; inspect its current configuration")
	}
	if p.Operation.Kind == "cron_delete" {
		err = m.client.DeleteCron(ctx, c.ID)
		return map[string]any{"deleted": err == nil}, err
	}
	var req api.UpdateCronRequest
	if err := json.Unmarshal(p.Operation.Request, &req); err != nil {
		return nil, err
	}
	return m.client.UpdateCron(ctx, c.ID, req)
}

func cronFingerprint(c api.CronResponse) string { c.LastFiredAt = ""; return resourceFingerprint(c) }
