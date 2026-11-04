package mcpmanagement

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// Native provisioning inputs and step checkpoints are sealed in the existing
// connection journal. No process, source directory or CLI output is required
// to resume. Polling is read-only; resumption is an explicit apply of the same ID.
type nativeProvision struct {
	Version         int        `json:"version"`
	Kind            string     `json:"kind"`
	Name            string     `json:"name,omitempty"`
	Permission      string     `json:"permission,omitempty"`
	Region          string     `json:"region,omitempty"`
	Access          string     `json:"access,omitempty"`
	Cron            cronSpec   `json:"cron,omitempty"`
	ResourceID      string     `json:"resource_id,omitempty"`
	CreationID      string     `json:"creation_id,omitempty"`
	BindingID       string     `json:"binding_id,omitempty"`
	Phase           string     `json:"phase"`
	StartedAt       time.Time  `json:"started_at,omitempty"`
	Created         bool       `json:"created,omitempty"`
	BindingCreated  bool       `json:"binding_created,omitempty"`
	Problem         *ToolError `json:"problem,omitempty"`
	CancelRequested bool       `json:"cancel_requested,omitempty"`
}

const provisionReceiptTTL = 24 * time.Hour

func (m *management) nativeProvisioning() bool { return m.opts.planStore != nil }

// Caller holds m.mu. Expiration is anchored to first admission, so repeated
// failures cannot indefinitely renew an old approved intent.
func (m *management) admitNativeProvisionLocked(p *releasePlan) error {
	n := p.Provision.Native
	if work := m.provisions[p.ID]; work == nil || work.status != "running" {
		n.CancelRequested = false
	}
	if n.CancelRequested {
		return context.Canceled
	}
	if n.StartedAt.IsZero() {
		n.StartedAt = time.Now().UTC()
	}
	p.ExpiresAt = n.StartedAt.Add(provisionReceiptTTL)
	return m.persistPlans()
}

func (n *nativeProvision) valid(p *releasePlan) bool {
	if n.Version != 1 || p.Action != "provision_"+n.Kind || p.Scope != api.DefaultEnvScope {
		return false
	}
	if n.CreationID != "" && (n.Kind != "postgres" || n.CreationID != provisionCreationID(p)) {
		return false
	}
	for _, id := range []string{n.ResourceID, n.BindingID} {
		if n.Kind != "domain" && id != "" && validateSegment(id, "resource_id") != nil {
			return false
		}
	}
	switch n.Kind {
	case "domain":
		return len(n.Name) <= 253 && domainName.MatchString(n.Name) && (n.ResourceID == "" || n.ResourceID == n.Name) && n.BindingID == ""
	case "bucket":
		return bucketName.MatchString(n.Name) && bucketAccess[n.Permission]
	case "postgres":
		return regionName.MatchString(n.Region) && postgresAccess[n.Access]
	case "cron":
		return n.Cron.Schedule != "" && len(n.Cron.Schedule) <= 128 && !strings.ContainsAny(n.Cron.Schedule, "\r\n") && strings.HasPrefix(n.Cron.Path, "/") && len(n.Cron.Path) <= 512 && !strings.ContainsAny(n.Cron.Path, " \r\n") && (n.Cron.Timezone == "" || timezoneName.MatchString(n.Cron.Timezone))
	}
	return false
}

func (m *management) provisionCheckpoint(p *releasePlan, phase string, update func(*nativeProvision)) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := p.Provision.Native
	if update == nil && n.Phase == phase && n.Problem == nil {
		return nil
	}
	if update != nil {
		update(n)
	}
	n.Phase, n.Problem = phase, nil
	return m.persistPlans()
}

func (m *management) provisionFailure(p *releasePlan, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	problem := m.toolError(err).StructuredContent.(ToolError)
	n := p.Provision.Native
	n.Problem = &problem
	if n.CancelRequested || errors.Is(err, context.Canceled) {
		n.Phase = "cancelled"
	} else {
		n.Phase = "interrupted"
	}
	// Retain the native resource IDs even if the caller's transport has gone.
	_ = m.persistPlans()
}

func provisionStepContext(ctx context.Context, p *releasePlan, step string) context.Context {
	return api.ContextWithIdempotencyKey(ctx, "mcp-provision-"+p.ID+"-"+step)
}

func provisionCreationID(p *releasePlan) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte("gregale-mcp-provision:"+p.ID)).String()
}

func (m *management) applyNativeProvision(ctx context.Context, p *releasePlan) (map[string]any, error) {
	n := p.Provision.Native
	if !n.valid(p) {
		return nil, invalid("invalid_plan", "invalid persisted provisioning inputs")
	}
	var out map[string]any
	var err error
	switch n.Kind {
	case "domain":
		out, err = m.nativeProvisionDomain(ctx, p)
	case "cron":
		out, err = m.nativeProvisionCron(ctx, p)
	case "bucket":
		out, err = m.nativeProvisionBucket(ctx, p)
	case "postgres":
		out, err = m.nativeProvisionPostgres(ctx, p)
	}
	if err != nil {
		m.provisionFailure(p, err)
	}
	return out, err
}

func (m *management) nativeProvisionDomain(ctx context.Context, p *releasePlan) (map[string]any, error) {
	n := p.Provision.Native
	if n.ResourceID == "" {
		// Domain creation conflicts on an existing claim. Read the identity
		// first so a lost create response can resume without claiming again.
		d, err := m.client.GetDomain(ctx, n.Name)
		if err == nil {
			if d.AppID != p.AppID || d.Domain != n.Name || normalizedScope(d.Environment) != p.Scope {
				return nil, invalid("plan_stale", "domain ownership changed")
			}
			if err := m.provisionCheckpoint(p, "inspecting", func(n *nativeProvision) { n.ResourceID = d.Domain }); err != nil {
				return nil, err
			}
			return domainProvisionView(d), nil
		}
		var problem *api.APIError
		if !errors.As(err, &problem) || problem.Problem.Status != 404 {
			return nil, err
		}
		if err := m.provisionCheckpoint(p, "creating", nil); err != nil {
			return nil, err
		}
		d, err = m.client.CreateDomain(provisionStepContext(ctx, p, "create"), api.CreateCustomDomainRequest{Domain: n.Name, AppID: p.AppID})
		if err != nil {
			return nil, err
		}
		if d.AppID != p.AppID || d.Domain != n.Name {
			return nil, invalid("invalid_response", "created domain identity mismatch")
		}
		if err := m.provisionCheckpoint(p, "inspecting", func(n *nativeProvision) { n.ResourceID = d.Domain; n.Created = true }); err != nil {
			return nil, err
		}
	}
	d, err := m.client.GetDomain(ctx, n.Name)
	if err != nil {
		return nil, err
	}
	if d.AppID != p.AppID || d.Domain != n.ResourceID || normalizedScope(d.Environment) != p.Scope {
		return nil, invalid("plan_stale", "domain ownership changed")
	}
	return domainProvisionView(d), nil
}

func domainProvisionView(d api.CustomDomainResponse) map[string]any {
	return map[string]any{"domain": d.Domain, "verified": d.Verified, "cert_status": d.CertStatus, "pending": !d.Verified || d.CertStatus != "issued", "dns_records": d.DNSRecords}
}

func (m *management) nativeProvisionCron(ctx context.Context, p *releasePlan) (map[string]any, error) {
	n := p.Provision.Native
	if n.ResourceID == "" {
		if err := m.provisionCheckpoint(p, "creating", nil); err != nil {
			return nil, err
		}
		c, err := m.client.CreateCron(provisionStepContext(ctx, p, "create"), p.App, api.CreateCronRequest{AppID: p.AppID, Schedule: n.Cron.Schedule, Path: n.Cron.Path, Timezone: n.Cron.Timezone})
		if err != nil {
			return nil, err
		}
		if c.AppID != p.AppID || c.ID == "" {
			return nil, invalid("invalid_response", "created cron identity mismatch")
		}
		if err := m.provisionCheckpoint(p, "inspecting", func(n *nativeProvision) { n.ResourceID = c.ID; n.Created = true }); err != nil {
			return nil, err
		}
	}
	c, err := m.client.GetCron(ctx, n.ResourceID)
	if err != nil {
		return nil, err
	}
	if c.ID != n.ResourceID || !provisionCronMatches(p, c) {
		return nil, invalid("plan_stale", "cron identity or configuration changed")
	}
	return jsonObject(c), nil
}

func provisionCronMatches(p *releasePlan, c api.CronResponse) bool {
	spec := p.Provision.Native.Cron
	timezone := spec.Timezone
	if timezone == "" {
		timezone = "UTC"
	}
	return c.AppID == p.AppID && c.Schedule == spec.Schedule && c.Path == spec.Path && c.Timezone == timezone &&
		c.Enabled && len(c.Command) == 0 && !c.SkipIfRunning && c.SchedulePolicy == nil && c.FailureRules == nil
}

func (m *management) nativeProvisionBucket(ctx context.Context, p *releasePlan) (map[string]any, error) {
	n := p.Provision.Native
	if n.ResourceID == "" {
		if err := m.provisionCheckpoint(p, "creating", nil); err != nil {
			return nil, err
		}
		b, err := m.client.CreateObjectBucket(provisionStepContext(ctx, p, "create"), p.App, api.CreateObjectBucketRequest{Name: n.Name, Scope: p.Scope})
		if err != nil {
			return nil, err
		}
		if b.ID == "" || b.Name != n.Name || normalizedScope(b.Scope) != p.Scope {
			return nil, invalid("invalid_response", "created bucket identity mismatch")
		}
		if err := m.provisionCheckpoint(p, "waiting_resource", func(n *nativeProvision) { n.ResourceID = b.ID; n.Created = true }); err != nil {
			return nil, err
		}
	}
	var bucket api.ObjectBucket
	if err := m.provisionCheckpoint(p, "waiting_resource", nil); err != nil {
		return nil, err
	}
	if err := waitProvision(ctx, func() (bool, error) {
		rows, err := m.client.ListObjectBuckets(ctx, p.App)
		if err != nil {
			return false, err
		}
		for _, b := range rows.Items {
			if b.ID != n.ResourceID {
				continue
			}
			if b.Name != n.Name || normalizedScope(b.Scope) != p.Scope {
				return false, invalid("plan_stale", "bucket scope or identity changed")
			}
			bucket = b
			return provisionReady(b.State)
		}
		return false, invalid("plan_stale", "the reviewed bucket no longer exists")
	}); err != nil {
		return nil, err
	}
	if n.BindingID == "" {
		if err := m.provisionCheckpoint(p, "binding", nil); err != nil {
			return nil, err
		}
		b, err := m.client.CreateObjectStorageComputeBinding(provisionStepContext(ctx, p, "bind"), p.App, n.ResourceID, api.CreateObjectStorageComputeBindingRequest{Label: "compute", Permission: n.Permission, Prefix: defaultProvisionBucketPrefix(n.Name)})
		if err != nil {
			return nil, err
		}
		if b.ID == "" || b.BucketID != n.ResourceID || normalizedScope(b.Scope) != p.Scope {
			return nil, invalid("invalid_response", "storage binding identity mismatch")
		}
		if err := m.provisionCheckpoint(p, "inspecting", func(n *nativeProvision) { n.BindingID = b.ID; n.BindingCreated = true }); err != nil {
			return nil, err
		}
	}
	rows, err := m.client.ListObjectStorageComputeBindings(ctx, p.App, n.ResourceID)
	if err != nil {
		return nil, err
	}
	for _, b := range rows.Items {
		if b.ID != n.BindingID {
			continue
		}
		if b.BucketID != n.ResourceID || normalizedScope(b.Scope) != p.Scope || b.Credential.Permission != n.Permission || b.Credential.Status == "revoked" {
			return nil, invalid("plan_stale", "storage binding changed")
		}
		return map[string]any{"app_id": p.AppID, "bucket": bucket, "binding": map[string]any{"id": b.ID, "bucket_id": b.BucketID, "scope": b.Scope, "prefix": b.Prefix, "permission": b.Credential.Permission, "rotation_pending": b.RotationPending}, "bucket_created": n.Created, "binding_created": n.BindingCreated}, nil
	}
	return nil, invalid("plan_stale", "storage binding no longer exists")
}

func (m *management) nativeProvisionPostgres(ctx context.Context, p *releasePlan) (map[string]any, error) {
	n := p.Provision.Native
	if n.ResourceID == "" {
		if err := m.provisionCheckpoint(p, "creating", func(n *nativeProvision) { n.CreationID = provisionCreationID(p) }); err != nil {
			return nil, err
		}
		name := strings.TrimSuffix(p.App, "-") + "-postgres"
		if len(name) > 63 {
			name = strings.TrimRight(name[:63], "-")
		}
		scaleToZero := true
		d, err := m.client.CreateManagedPostgresDatabase(provisionStepContext(ctx, p, "create"), api.CreateManagedPostgresDatabaseRequest{RequestID: n.CreationID, Name: name, Region: n.Region, PostgresMajor: 16, ServiceClass: "development", Availability: "single_zone", ScaleToZero: &scaleToZero})
		if err != nil {
			return nil, err
		}
		if d.ID != n.CreationID || d.Name != name || d.Region != n.Region {
			return nil, invalid("invalid_response", "created database identity mismatch")
		}
		if err := m.provisionCheckpoint(p, "waiting_resource", func(n *nativeProvision) { n.ResourceID = d.ID; n.Created = true }); err != nil {
			return nil, err
		}
	}
	var database api.ManagedPostgresDatabase
	if err := m.provisionCheckpoint(p, "waiting_resource", nil); err != nil {
		return nil, err
	}
	if err := waitProvision(ctx, func() (bool, error) {
		d, err := m.client.GetManagedPostgresDatabase(ctx, n.ResourceID)
		if err != nil {
			return false, err
		}
		if d.ID != n.ResourceID || d.Region != n.Region {
			return false, invalid("plan_stale", "database identity or region changed")
		}
		database = d
		return provisionReady(d.State)
	}); err != nil {
		return nil, err
	}
	if n.BindingID == "" {
		if err := m.provisionCheckpoint(p, "binding", nil); err != nil {
			return nil, err
		}
		b, err := m.client.CreateManagedPostgresBinding(provisionStepContext(ctx, p, "bind"), n.ResourceID, api.CreateManagedPostgresBindingRequest{AppID: p.AppID, Scope: p.Scope, EnvironmentKey: "DATABASE_URL", Access: n.Access})
		if err != nil {
			return nil, err
		}
		if !provisionPostgresBindingMatches(p, b) {
			return nil, invalid("invalid_response", "created database binding identity mismatch")
		}
		if err := m.provisionCheckpoint(p, "waiting_binding", func(n *nativeProvision) { n.BindingID = b.ID; n.BindingCreated = true }); err != nil {
			return nil, err
		}
	}
	var binding api.ManagedPostgresBinding
	if err := m.provisionCheckpoint(p, "waiting_binding", nil); err != nil {
		return nil, err
	}
	if err := waitProvision(ctx, func() (bool, error) {
		b, err := m.client.GetManagedPostgresBinding(ctx, n.BindingID)
		if err != nil {
			return false, err
		}
		if b.ID != n.BindingID || !provisionPostgresBindingMatches(p, b) {
			return false, invalid("plan_stale", "database binding changed")
		}
		binding = b
		return provisionReady(b.State)
	}); err != nil {
		return nil, err
	}
	// Managed binding egress is narrowed to the database addresses by native
	// admission; do not overwrite the app's general egress-port configuration.
	return map[string]any{"app_id": p.AppID, "database": database, "binding": binding, "database_created": n.Created}, nil
}

func provisionPostgresBindingMatches(p *releasePlan, b api.ManagedPostgresBinding) bool {
	return b.ID != "" && b.AppID == p.AppID && b.DatabaseID == p.Provision.Native.ResourceID && normalizedScope(b.Scope) == p.Scope && b.EnvironmentKey == "DATABASE_URL" && b.Access == p.Provision.Native.Access
}

func provisionReady(state string) (bool, error) {
	if state == "failed" || state == "deleted" || state == "deleting" || state == "suspended" {
		return false, invalid("provision_failed", "native resource is "+state)
	}
	return state == "ready", nil
}

func waitProvision(ctx context.Context, read func() (bool, error)) error {
	for {
		if ready, err := read(); err != nil || ready {
			return err
		}
		timer := time.NewTimer(2 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func jsonObject(v any) map[string]any {
	raw, _ := json.Marshal(v)
	var out map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	_ = decoder.Decode(&out)
	return out
}

func (m *management) planNativeBucket(ctx context.Context, p *releasePlan) error {
	n := p.Provision.Native
	rows, err := m.client.ListObjectBuckets(ctx, p.App)
	if err != nil {
		return err
	}
	for _, b := range rows.Items {
		if b.Name != n.Name || normalizedScope(b.Scope) != p.Scope {
			continue
		}
		if n.ResourceID != "" {
			return invalid("resource_conflict", "more than one bucket matches the reviewed name")
		}
		n.ResourceID = b.ID
	}
	if n.ResourceID == "" {
		return nil
	}
	bindings, err := m.client.ListObjectStorageComputeBindings(ctx, p.App, n.ResourceID)
	if err != nil {
		return err
	}
	for _, b := range bindings.Items {
		if b.BucketID != n.ResourceID {
			return invalid("invalid_response", "storage binding bucket identity mismatch")
		}
		if normalizedScope(b.Scope) == p.Scope && b.Prefix == defaultProvisionBucketPrefix(n.Name) {
			if b.Credential.Permission != n.Permission || b.Credential.Status == "revoked" {
				return invalid("resource_conflict", "existing compute binding has different permissions or was revoked")
			}
			n.BindingID = b.ID
		}
	}
	return nil
}

func defaultProvisionBucketPrefix(name string) string {
	prefix := "GREGALE_S3_" + strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
	if len(prefix) > 48 {
		prefix = prefix[:48]
	}
	return strings.TrimRight(prefix, "_")
}

func (m *management) planNativePostgres(ctx context.Context, p *releasePlan) error {
	n := p.Provision.Native
	name := strings.TrimRight(p.App, "-") + "-postgres"
	if len(name) > 63 {
		name = strings.TrimRight(name[:63], "-")
	}
	rows, err := m.client.ListManagedPostgresDatabases(ctx)
	if err != nil {
		return err
	}
	for _, d := range rows.Items {
		if d.Name != name || d.State == "deleted" {
			continue
		}
		if n.ResourceID != "" || d.Region != n.Region {
			return invalid("resource_conflict", "existing database name or region differs from this plan")
		}
		bindings, err := m.client.ListManagedPostgresBindings(ctx, d.ID)
		if err != nil {
			return err
		}
		for _, b := range bindings.Items {
			if b.AppID == p.AppID && normalizedScope(b.Scope) == p.Scope && b.EnvironmentKey == "DATABASE_URL" && b.Access == n.Access && b.State != "deleted" {
				n.ResourceID, n.BindingID = d.ID, b.ID
			}
		}
		if n.ResourceID == "" {
			return invalid("resource_conflict", "a database with this name exists without the reviewed app binding; inspect it before attaching it explicitly")
		}
	}
	return nil
}
