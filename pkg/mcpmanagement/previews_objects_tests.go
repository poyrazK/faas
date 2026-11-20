package mcpmanagement

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

// Pull-request previews, object reads and saved workflow tests
// (ADR-20261118140000). Previews belong to their consented parent app.
// Object contents are customer data: reading them needs data:read consent,
// and the signed URL that fetches them never reaches the agent. Saved
// workflow tests are simulations with recorded mocks, so running them calls
// nothing outside Gregale.

const (
	defaultObjectReadBytes = 64 << 10
	maxObjectReadBytes     = 256 << 10
	maxObjectKeyBytes      = 1024
)

type previewArgs struct {
	App     string `json:"app" jsonschema:"Parent app slug"`
	Preview string `json:"preview" jsonschema:"Preview app slug from gregale_list_previews"`
}

type planPreviewArgs struct {
	App      string `json:"app" jsonschema:"Parent app slug"`
	Op       string `json:"op" jsonschema:"create or destroy"`
	PRNumber int    `json:"pr_number,omitempty" jsonschema:"create: pull request number"`
	TTLHours int    `json:"ttl_hours,omitempty" jsonschema:"create: hours until the preview expires; default from the app's GitHub policy"`
	Preview  string `json:"preview,omitempty" jsonschema:"destroy: preview app slug"`
	Reason   string `json:"reason" jsonschema:"Reviewable reason; at most 280 characters"`
}

type previewChangeRequest struct {
	Op       string `json:"op"`
	PRNumber int    `json:"pr_number,omitempty"`
	TTLHours int    `json:"ttl_hours,omitempty"`
	Preview  string `json:"preview,omitempty"`
}

type objectListArgs struct {
	App    string `json:"app" jsonschema:"App slug"`
	Bucket string `json:"bucket" jsonschema:"Bucket name from gregale_list_buckets"`
	Prefix string `json:"prefix,omitempty" jsonschema:"Only keys starting with this"`
	Limit  int    `json:"limit,omitempty" jsonschema:"1 to 100; default 20"`
	Cursor string `json:"cursor,omitempty" jsonschema:"next_cursor from the previous page"`
}

type objectReadArgs struct {
	App      string `json:"app" jsonschema:"App slug"`
	Bucket   string `json:"bucket" jsonschema:"Bucket name"`
	Key      string `json:"key" jsonschema:"Object key"`
	MaxBytes int    `json:"max_bytes,omitempty" jsonschema:"Read at most this many bytes from the start: 1 to 262144; default 65536"`
}

type workflowTestsArgs struct {
	App      string `json:"app" jsonschema:"App slug"`
	Workflow string `json:"workflow" jsonschema:"Saved workflow name"`
}

func registerPreviewsObjectsTests(s *mcp.Server, m *management) {
	register(s, m, "gregale_list_previews", "List the app's pull-request previews: slug, pull request, state, latest deployment and expiry. Read-only.", m.listPreviews)
	register(s, m, "gregale_get_preview", "Read one pull-request preview: its deployment, what differs from production and its links. Read-only.", m.getPreview)
	register(s, m, "gregale_plan_preview_change", "Review creating a pull-request preview of the app, or destroying one. Changes nothing; apply with gregale_apply_operation. Destroying asks a person first.", m.planPreviewChange)
	register(s, m, "gregale_list_objects", "List objects in one of the app's buckets: keys, sizes and times, filtered by prefix. Read-only; no contents.", m.listObjects)
	register(s, m, "gregale_read_object", "Read the start of one object in the app's bucket, up to 256 KiB, as text or base64. Needs data:read consent. Read-only.", m.readObject)
	register(s, m, "gregale_list_workflow_tests", "List the app's saved workflows with their saved test cases and last test report. Read-only.", m.listWorkflowTests)
	if m.opts.AllowWrites {
		registerTool(s, m, "gregale_run_workflow_tests", "Run a saved workflow's test cases against its current draft. Tests are simulations with recorded mocks: they call nothing outside Gregale. Saves and returns the report.", false, m.runWorkflowTests)
	}
}

func previewSummary(a api.AppResponse) map[string]any {
	return map[string]any{"slug": a.Slug, "pr_number": a.PreviewPRNumber, "status": a.Status, "expires_at": a.PreviewExpiresAt, "url": a.URL}
}

func (m *management) listPreviews(ctx context.Context, a appArgs) (any, error) {
	if _, err := m.consentedApp(ctx, a.App, false); err != nil {
		return nil, err
	}
	apps, err := m.client.ListApps(ctx)
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for _, app := range apps {
		if app.PreviewOfSlug == a.App {
			out = append(out, previewSummary(app))
		}
	}
	return map[string]any{"app": a.App, "previews": out}, nil
}

// previewOf returns a preview of the consented parent app.
func (m *management) previewOf(ctx context.Context, parent, preview string, write bool) (api.PreviewResourceResponse, error) {
	if err := validateSegment(preview, "preview"); err != nil {
		return api.PreviewResourceResponse{}, err
	}
	if _, err := m.consentedApp(ctx, parent, write); err != nil {
		return api.PreviewResourceResponse{}, err
	}
	p, err := m.client.GetPreview(ctx, preview)
	if err != nil {
		return p, err
	}
	if p.App.Slug != preview || p.App.PreviewOfSlug != parent {
		return p, invalid("app_access_denied", "the preview belongs to a different app")
	}
	return p, nil
}

func (m *management) getPreview(ctx context.Context, a previewArgs) (any, error) {
	return m.previewOf(ctx, a.App, a.Preview, false)
}

func (m *management) planPreviewChange(ctx context.Context, a planPreviewArgs) (any, error) {
	req := previewChangeRequest{Op: a.Op}
	view := map[string]any{}
	switch a.Op {
	case "create":
		if a.PRNumber < 1 || a.TTLHours < 0 || a.Preview != "" {
			return nil, invalid("invalid_argument", "create needs pr_number, a ttl_hours that is not negative, and no preview")
		}
		req.PRNumber, req.TTLHours = a.PRNumber, a.TTLHours
	case "destroy":
		if a.PRNumber != 0 || a.TTLHours != 0 {
			return nil, invalid("invalid_argument", "destroy takes only preview")
		}
		p, err := m.previewOf(ctx, a.App, a.Preview, false)
		if err != nil {
			return nil, err
		}
		req.Preview = a.Preview
		view["preview"] = previewSummary(p.App)
	default:
		return nil, invalid("invalid_argument", "op must be create or destroy")
	}
	p, err := m.operationBase(ctx, operationArgs{App: a.App, Reason: a.Reason}, "preview_change", req)
	if err != nil {
		return nil, err
	}
	view["request"] = req
	p.Operation.Changes, _ = json.Marshal(view)
	p.TrafficEffect = map[string]string{
		"create":  "Creates or reopens the pull request's preview app, ready for a deployment. Production is untouched.",
		"destroy": "Deletes the preview app with its deployments and data. Production is untouched.",
	}[a.Op]
	return m.savePlan(p)
}

// runPreviewChange applies a preview plan. A retried destroy skips the
// ownership check (planning checked it, and the first attempt may already
// have destroyed the preview) and takes not-found as done.
func (m *management) runPreviewChange(ctx context.Context, p *releasePlan, retry bool) (any, error) {
	var req previewChangeRequest
	if err := json.Unmarshal(p.Operation.Request, &req); err != nil {
		return nil, err
	}
	ctx = api.ContextWithIdempotencyKey(ctx, "mcp-operation-"+p.ID)
	switch req.Op {
	case "create":
		app, err := m.client.CreatePreview(ctx, p.App, api.CreatePreviewRequest{PRNumber: req.PRNumber, TTLHours: req.TTLHours})
		if err != nil {
			return nil, err
		}
		return previewSummary(app), nil
	case "destroy":
		if !retry {
			if _, err := m.previewOf(ctx, p.App, req.Preview, true); err != nil {
				return nil, err
			}
		}
		if err := m.client.DestroyPreview(ctx, req.Preview); err != nil {
			var native *api.APIError
			if !retry || !errors.As(err, &native) || native.Problem.Status != http.StatusNotFound {
				return nil, err
			}
		}
		return map[string]any{"destroyed": req.Preview}, nil
	}
	return nil, invalid("invalid_plan", "unsupported preview change")
}

func validObjectKey(key, field string, empty bool) error {
	if key == "" && empty {
		return nil
	}
	if key == "" || len(key) > maxObjectKeyBytes || !utf8.ValidString(key) || strings.ContainsFunc(key, unicode.IsControl) {
		return invalid("invalid_argument", fmt.Sprintf("%s must be 1 to %d bytes of UTF-8 without control characters", field, maxObjectKeyBytes))
	}
	return nil
}

// appBucket checks the bucket belongs to the consented app.
func (m *management) appBucket(ctx context.Context, app, bucket string) error {
	if err := validateSegment(bucket, "bucket"); err != nil {
		return err
	}
	if _, err := m.consentedApp(ctx, app, false); err != nil {
		return err
	}
	list, err := m.client.ListObjectBuckets(ctx, app)
	if err != nil {
		return err
	}
	for _, b := range list.Items {
		if b.Name == bucket {
			return nil
		}
	}
	return invalid("app_access_denied", "the bucket is not one of the app's buckets")
}

func (m *management) listObjects(ctx context.Context, a objectListArgs) (any, error) {
	limit, err := pageSize(a.Limit)
	if err != nil {
		return nil, err
	}
	if err := validObjectKey(a.Prefix, "prefix", true); err != nil {
		return nil, err
	}
	if err := validObjectKey(a.Cursor, "cursor", true); err != nil {
		return nil, err
	}
	if err := m.appBucket(ctx, a.App, a.Bucket); err != nil {
		return nil, err
	}
	return m.client.ListBucketObjects(ctx, a.App, a.Bucket, a.Prefix, a.Cursor, limit)
}

func (m *management) readObject(ctx context.Context, a objectReadArgs) (any, error) {
	if !m.hasCapability(api.ScopeDataRead) {
		return nil, invalid("data_read_required", "reading object contents needs data:read consent; reconnect and grant it")
	}
	limit := a.MaxBytes
	if limit == 0 {
		limit = defaultObjectReadBytes
	}
	if limit < 1 || limit > maxObjectReadBytes {
		return nil, invalid("invalid_argument", "max_bytes must be from 1 to 262144")
	}
	if err := validObjectKey(a.Key, "key", false); err != nil {
		return nil, err
	}
	if err := m.appBucket(ctx, a.App, a.Bucket); err != nil {
		return nil, err
	}
	signed, err := m.client.SignBucketObject(ctx, a.App, a.Bucket, api.ObjectSignRequest{Method: http.MethodGet, Key: a.Key, ExpiresIn: 60})
	if err != nil {
		return nil, err
	}
	return fetchObjectStart(ctx, signed, limit)
}

// fetchObjectStart reads up to limit bytes through a signed GET. The URL is
// a bearer link, so it stays out of the result and out of errors.
func fetchObjectStart(ctx context.Context, signed api.ObjectSignedRequest, limit int) (any, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, signed.URL, nil)
	if err != nil {
		return nil, invalid("invalid_response", "object store returned an unusable link")
	}
	for k, v := range signed.Headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=0-%d", limit-1))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, invalid("object_read_failed", "the object store did not answer")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return nil, invalid("object_read_failed", fmt.Sprintf("the object store answered %d", resp.StatusCode))
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, int64(limit)+1))
	if err != nil {
		return nil, invalid("object_read_failed", "the object read was cut off")
	}
	truncated := len(body) > limit || resp.StatusCode == http.StatusPartialContent && resp.Header.Get("Content-Range") != fmt.Sprintf("bytes 0-%d/%d", len(body)-1, len(body))
	if len(body) > limit {
		body = body[:limit]
	}
	out := map[string]any{"content_type": resp.Header.Get("Content-Type"), "bytes": len(body), "truncated": truncated}
	if utf8.Valid(body) {
		out["text"] = string(body)
	} else {
		out["base64"] = base64.StdEncoding.EncodeToString(body)
	}
	return out, nil
}

func (m *management) listWorkflowTests(ctx context.Context, a appArgs) (any, error) {
	if _, err := m.consentedApp(ctx, a.App, false); err != nil {
		return nil, err
	}
	list, err := m.client.ListStoredWorkflows(ctx, a.App)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(list.StoredWorkflows))
	for _, w := range list.StoredWorkflows {
		cases := []string{}
		if w.TestSuite != nil {
			for _, c := range w.TestSuite.Cases {
				cases = append(cases, c.Name)
			}
		}
		out = append(out, map[string]any{"workflow": w.Name, "version": w.Version, "test_cases": cases, "last_report": w.TestReport})
	}
	return map[string]any{"app": a.App, "workflows": out}, nil
}

func (m *management) runWorkflowTests(ctx context.Context, a workflowTestsArgs) (any, error) {
	if err := validateSegment(a.Workflow, "workflow"); err != nil {
		return nil, err
	}
	if _, err := m.consentedApp(ctx, a.App, true); err != nil {
		return nil, err
	}
	w, err := m.client.GetStoredWorkflow(ctx, a.App, a.Workflow)
	if err != nil {
		return nil, err
	}
	if w.TestSuite == nil || len(w.TestSuite.Cases) == 0 {
		return nil, invalid("no_tests", "the workflow has no saved test cases; save some with gregale workflow test")
	}
	return m.client.RunStoredWorkflowTests(ctx, a.App, a.Workflow, api.RunStoredWorkflowTestsRequest{ExpectedVersion: w.Version})
}
