package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/onebox-faas/faas/pkg/rehearsalobj"
)

type testObjectFixture struct {
	Key         string            `yaml:"key" json:"key"`
	File        string            `yaml:"file" json:"file"`
	ContentType string            `yaml:"content_type,omitempty" json:"content_type,omitempty"`
	Metadata    map[string]string `yaml:"metadata,omitempty" json:"metadata,omitempty"`
}
type testObjectSpec struct {
	Version  int                 `yaml:"version" json:"version"`
	Bucket   string              `yaml:"bucket" json:"bucket"`
	Fixtures []testObjectFixture `yaml:"fixtures" json:"fixtures"`
}

func testObjectOwnedEnv(key string) bool {
	switch strings.ToUpper(key) {
	case "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_REGION", "AWS_DEFAULT_REGION", "AWS_ENDPOINT_URL_S3", "AWS_ENDPOINT_URL", "AWS_SESSION_TOKEN", "S3_BUCKET":
		return true
	}
	return false
}

func validateTestObjects(s testScenario) error {
	if s.ObjectsFixture == nil {
		return nil
	}
	p := s.ObjectsFixture
	if s.CommandExecution == nil || len(s.Buckets) > 0 || len(s.Cleanup) > 0 {
		return errors.New("objects_fixture requires contained commands, platform-owned cleanup and no ordinary bucket bindings")
	}
	for _, values := range []map[string]string{s.Secrets, s.CommandExecution.Env} {
		for key := range values {
			if testObjectOwnedEnv(key) {
				return errors.New("objects_fixture owns its S3 endpoint, bucket and AWS credentials; overrides are unsupported")
			}
		}
	}
	probe := make([]rehearsalobj.Data, 0, len(p.Fixtures))
	for _, f := range p.Fixtures {
		if !filepath.IsLocal(f.File) || f.File == "." || path.Clean(f.File) != f.File || strings.Contains(f.File, "\\") {
			return errors.New("object fixture must be a clean relative file path")
		}
		contentType := f.ContentType
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		probe = append(probe, rehearsalobj.Data{Key: f.Key, ContentType: contentType, Metadata: f.Metadata})
	}
	if _, e := rehearsalobj.Snapshot(probe); e != nil {
		return e
	}
	return (rehearsalobj.Spec{Version: p.Version, Bucket: p.Bucket, FixtureSHA256: strings.Repeat("a", 64)}).Validate()
}

type testObjectClient interface {
	CreateScenarioObjects(context.Context, string, rehearsalobj.CreateRequest) (rehearsalobj.Receipt, error)
	SealScenarioObjects(context.Context, string, string) (rehearsalobj.Evidence, error)
	DeleteScenarioObjects(context.Context, string) error
}
type testObjectSession struct {
	request rehearsalobj.CreateRequest
	receipt rehearsalobj.Receipt
	created bool
}

func prepareTestObjects(source string, s testScenario, r *testRunReceipt) (*testObjectSession, error) {
	if s.ObjectsFixture == nil {
		return nil, nil
	}
	if e := validateTestObjects(s); e != nil {
		return nil, e
	}
	r.Objects = &rehearsalobj.Evidence{Receipt: rehearsalobj.Receipt{Version: 1, RunID: r.RunID}, Status: "incomplete", Provenance: rehearsalobj.Provenance, Reason: "object fixture execution did not complete"}
	objects := make([]rehearsalobj.Data, 0, len(s.ObjectsFixture.Fixtures))
	total := 0
	for _, f := range s.ObjectsFixture.Fixtures {
		body, e := readTestObjectFixture(source, f.File)
		if e != nil {
			return nil, e
		}
		total += len(body)
		if total > rehearsalobj.MaxContentBytes {
			return nil, errors.New("object fixtures exceed the content bound")
		}
		contentType := f.ContentType
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		objects = append(objects, rehearsalobj.Data{Key: f.Key, Body: body, ContentType: contentType, Metadata: f.Metadata})
	}
	initial, e := rehearsalobj.Snapshot(objects)
	if e != nil {
		return nil, e
	}
	p := s.ObjectsFixture
	return &testObjectSession{request: rehearsalobj.CreateRequest{Spec: rehearsalobj.Spec{Version: p.Version, Bucket: p.Bucket, FixtureSHA256: rehearsalobj.Hash(initial)}, Objects: objects}}, nil
}
func readTestObjectFixture(source, name string) ([]byte, error) {
	root, e := os.OpenRoot(source)
	if e != nil {
		return nil, e
	}
	defer func() { _ = root.Close() }()
	parts := strings.Split(name, "/")
	for i := range parts {
		info, e := root.Lstat(strings.Join(parts[:i+1], "/"))
		if e != nil || info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("object fixtures cannot traverse symlinks")
		}
	}
	f, e := root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		return nil, e
	}
	defer func() { _ = f.Close() }()
	info, e := f.Stat()
	if e != nil || !info.Mode().IsRegular() {
		return nil, errors.New("object fixtures must be regular files")
	}
	body, e := io.ReadAll(io.LimitReader(f, rehearsalobj.MaxObjectBytes+1))
	if e != nil {
		return nil, e
	}
	if len(body) > rehearsalobj.MaxObjectBytes {
		return nil, errors.New("object fixture exceeds 64 KiB")
	}
	return body, nil
}
func (p *testObjectSession) create(ctx context.Context, c testObjectClient, r *testRunReceipt, app string) error {
	if p == nil {
		return nil
	}
	p.created = true
	p.request.AppSlug = r.AppSlug
	ack, e := c.CreateScenarioObjects(ctx, r.RunID, p.request)
	if e != nil {
		return e
	}
	p.receipt = ack
	if ack.RunID != r.RunID || ack.CollectorID == "" || ack.AppID != app || ack.ResourceID == "" || ack.FixtureSHA256 != p.request.Spec.FixtureSHA256 || ack.ContractSHA256 != p.request.Spec.ContractSHA256() || ack.InitialSHA256 != p.request.Spec.FixtureSHA256 {
		return errors.New("object fixture identity does not match the test workload")
	}
	if r.Objects != nil {
		r.Objects.Receipt = ack
	}
	return nil
}
func (p *testObjectSession) finish(ctx context.Context, c testObjectClient, r *testRunReceipt) {
	if p == nil || !p.created {
		return
	}
	if r.Actions == nil || r.Actions.Evidence == nil || r.Actions.Evidence.Status != "complete" {
		r.Error = joinTestPostgresError(r.Error, "object evidence requires complete action evidence")
		r.Status = "failed"
		return
	}
	e, err := c.SealScenarioObjects(ctx, r.RunID, r.DeploymentID)
	if err != nil || e.Validate() != nil || e.ResourceID != p.receipt.ResourceID || e.CollectorID != p.receipt.CollectorID || e.InitialSHA256 != p.receipt.InitialSHA256 || e.ContractSHA256 != p.request.Spec.ContractSHA256() || e.FixtureSHA256 != p.request.Spec.FixtureSHA256 || e.RunID != r.RunID || e.AppID != p.receipt.AppID || e.DeploymentID != r.DeploymentID {
		r.Error = joinTestPostgresError(r.Error, "independent object evidence was incomplete")
		r.Status = "failed"
		return
	}
	r.Objects = &e
}
func (p *testObjectSession) cleanup(ctx context.Context, c testObjectClient, r *testRunReceipt) {
	if p == nil || !p.created {
		return
	}
	if e := c.DeleteScenarioObjects(ctx, r.RunID); e != nil {
		r.addCleanupError("object fixture cleanup was not confirmed")
		if r.Objects != nil {
			r.Objects.Status = "incomplete"
			r.Objects.CleanupConfirmed = false
			r.Objects.Reason = "fixture cleanup was not confirmed"
		}
	}
}
func compareTestObjects(before, after testRunReceipt) *rehearsalobj.Comparison {
	c := rehearsalobj.Compare(before.Objects, after.Objects)
	if c != nil && (!testExecutionCompleteForEvidence(before) || !testExecutionCompleteForEvidence(after)) {
		c.Status = "inconclusive"
		c.Reason = "one or both executions or cleanup steps did not pass"
	}
	return c
}
func testObjectComparisonStatus(runs []testRunComparison) string {
	status := ""
	for _, r := range runs {
		if r.Objects == nil {
			continue
		}
		if r.Objects.Status == "inconclusive" {
			return "inconclusive"
		}
		if status == "" || r.Objects.Status == "failed" {
			status = r.Objects.Status
		}
	}
	return status
}
func printTestObjectComparison(out io.Writer, c *rehearsalobj.Comparison) {
	if c == nil {
		return
	}
	_, _ = fmt.Fprintf(out, "  Objects: %s (platform collector; %d → %d mutations)\n", strings.ToUpper(c.Status), c.BeforeMutations, c.AfterMutations)
	if c.Reason != "" {
		_, _ = fmt.Fprintf(out, "    %s\n", c.Reason)
	}
	for _, m := range c.MutationChanges {
		_, _ = fmt.Fprintf(out, "    %s %q: %d → %d attempts\n", m.Method, testObjectMutationKey(m.Workload, m.Key), m.BeforeCount, m.AfterCount)
	}
	if c.Status == "failed" {
		for _, group := range []struct {
			name    string
			objects []rehearsalobj.Object
		}{{"baseline", c.BeforeObjects}, {"candidate", c.AfterObjects}} {
			for _, o := range group.objects {
				_, _ = fmt.Fprintf(out, "    %s %q: %d bytes sha256=%s content_type=%q metadata=%s\n", group.name, o.Key, o.Size, o.SHA256, o.ContentType, testObjectMetadata(o.Metadata))
			}
		}
	}
}
func renderTestObjectComparisonMarkdown(out io.Writer, c testReportComparison) {
	if c.ObjectStatus == "" {
		return
	}
	_, _ = fmt.Fprintf(out, "## Object storage comparison: %s\n\nIndependent final objects and mutation attempts, including duplicate writes.\n\n", strings.ToUpper(c.ObjectStatus))
	for _, r := range c.Runs {
		if r.Objects == nil {
			continue
		}
		o := r.Objects
		_, _ = fmt.Fprintf(out, "**%s:** %s · %d → %d mutations\n\n%s\n\n", markdownComparisonText(r.Key), markdownComparisonText(o.Status), o.BeforeMutations, o.AfterMutations, markdownComparisonText(o.Reason))
		for _, m := range o.MutationChanges {
			_, _ = fmt.Fprintf(out, "- %s %s: %d → %d attempts\n", m.Method, markdownComparisonText(testObjectMutationKey(m.Workload, m.Key)), m.BeforeCount, m.AfterCount)
		}
		_, _ = fmt.Fprintln(out, "\n| Revision | Key | Bytes | SHA-256 | Content type | Metadata |\n|---|---|---:|---|---|---|")
		for _, group := range []struct {
			name    string
			objects []rehearsalobj.Object
		}{{"baseline", o.BeforeObjects}, {"candidate", o.AfterObjects}} {
			for _, v := range group.objects {
				_, _ = fmt.Fprintf(out, "| %s | %s | %d | %s | %s | %s |\n", group.name, markdownComparisonText(v.Key), v.Size, v.SHA256, markdownComparisonText(v.ContentType), markdownComparisonText(testObjectMetadata(v.Metadata)))
			}
		}
		_, _ = fmt.Fprintln(out)
	}
}

func testObjectMetadata(v map[string]string) string { b, _ := json.Marshal(v); return string(b) }

func testObjectMutationKey(workload, key string) string {
	if workload == "" {
		return key
	}
	return workload + ": " + key
}
