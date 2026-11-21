package mcpmanagement

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/jobresult"
)

// A protocol output bound, independent of native job quotas/log retention.
const executionLogBytes = 16 << 10

type jobTaskArgs struct {
	Job       string `json:"job"`
	RunID     string `json:"run_id"`
	TaskIndex int    `json:"task_index" jsonschema:"Zero-based task index"`
}

type jobLogsArgs struct {
	Job       string `json:"job"`
	RunID     string `json:"run_id"`
	TaskIndex int    `json:"task_index" jsonschema:"Zero-based task index"`
	MaxBytes  int    `json:"max_bytes,omitempty" jsonschema:"1 to 16384 bytes; default 16384"`
}

type jobArtifactArgs struct {
	Job       string `json:"job"`
	RunID     string `json:"run_id"`
	TaskIndex int    `json:"task_index" jsonschema:"Zero-based task index"`
	Name      string `json:"name" jsonschema:"Exact artifact name from gregale_list_artifacts"`
}

type jobTaskView struct {
	TaskIndex   int    `json:"task_index"`
	Status      string `json:"status"`
	Attempt     int    `json:"attempt"`
	ErrorClass  string `json:"error_class,omitempty"`
	OutcomeCode string `json:"outcome_code,omitempty"`
	ExitCode    *int   `json:"exit_code,omitempty"`
}

type jobArtifactView struct {
	Name      string `json:"name"`
	SizeBytes int64  `json:"size_bytes"`
	SHA256    string `json:"sha256"`
	Managed   bool   `json:"managed"`
}

func (m *management) listJobTasks(ctx context.Context, a jobTasksArgs) (any, error) {
	limit, err := executionPage(a.Limit, a.Offset)
	if err != nil {
		return nil, err
	}
	j, _, err := m.executionRun(ctx, jobRunArgs{Job: a.Job, RunID: a.RunID})
	if err != nil {
		return nil, err
	}
	p, err := m.client.ListJobRunTasksPage(ctx, a.Job, a.RunID, limit, a.Offset)
	if err != nil {
		return nil, err
	}
	if p.Limit != limit || p.Offset != a.Offset || !validExecutionPage(limit, a.Offset, p.NextOffset, len(p.Tasks)) {
		return nil, invalid("invalid_response", "task pagination differs from the requested page")
	}
	items := make([]jobTaskView, 0, len(p.Tasks))
	for i, task := range p.Tasks {
		if task.RunID != a.RunID || task.TaskIndex != a.Offset+i {
			return nil, invalid("invalid_response", "task identity differs from the selected run and page")
		}
		v := jobTaskView{TaskIndex: task.TaskIndex, Status: task.Status, Attempt: task.Attempt, ErrorClass: task.ErrorClass, OutcomeCode: task.OutcomeCode}
		// The native DTO omits zero. Only success establishes a reported zero;
		// other zero-valued exits stay unknown rather than implying success.
		if task.Status == "succeeded" || task.ExitCode != 0 {
			v.ExitCode = &task.ExitCode
		}
		items = append(items, v)
	}
	if err := m.recheckJob(ctx, j); err != nil {
		return nil, err
	}
	return map[string]any{"schema_version": 1, "job": a.Job, "run_id": a.RunID, "items": items, "limit": limit, "offset": a.Offset, "next_offset": p.NextOffset}, nil
}

func (m *management) getJobLogs(ctx context.Context, a jobLogsArgs) (any, error) {
	if a.TaskIndex < 0 || a.MaxBytes < 0 || a.MaxBytes > executionLogBytes {
		return nil, invalid("invalid_argument", "task_index must be non-negative and max_bytes must be 1..16384 when supplied")
	}
	if a.MaxBytes == 0 {
		a.MaxBytes = executionLogBytes
	}
	j, r, err := m.executionRun(ctx, jobRunArgs{Job: a.Job, RunID: a.RunID})
	if err != nil {
		return nil, err
	}
	if a.TaskIndex >= r.Tasks {
		return nil, invalid("invalid_argument", "task_index is outside the selected run")
	}
	out, err := m.client.GetJobTaskLogsWithMaxBytes(ctx, a.Job, a.RunID, a.TaskIndex, a.MaxBytes)
	if err != nil {
		return nil, err
	}
	if len(out.LogContent) > a.MaxBytes || out.MaxBytes != a.MaxBytes {
		return nil, invalid("invalid_response", "job logs exceed the requested native bound")
	}
	if err := m.recheckJob(ctx, j); err != nil {
		return nil, err
	}
	return map[string]any{"schema_version": 1, "job": a.Job, "run_id": a.RunID, "task_index": a.TaskIndex, "task_status": out.TaskStatus,
		"log_content": out.LogContent, "truncated": out.Truncated, "max_bytes": out.MaxBytes}, nil
}

func (m *management) taskManifest(ctx context.Context, a jobTaskArgs) (api.JobResponse, api.JobTaskResponse, jobresult.Manifest, error) {
	if a.TaskIndex < 0 {
		return api.JobResponse{}, api.JobTaskResponse{}, jobresult.Manifest{}, invalid("invalid_argument", "task_index must be non-negative")
	}
	j, r, err := m.executionRun(ctx, jobRunArgs{Job: a.Job, RunID: a.RunID})
	if err != nil {
		return j, api.JobTaskResponse{}, jobresult.Manifest{}, err
	}
	if a.TaskIndex >= r.Tasks {
		return j, api.JobTaskResponse{}, jobresult.Manifest{}, invalid("invalid_argument", "task_index is outside the selected run")
	}
	p, err := m.client.ListJobRunTasksPage(ctx, a.Job, a.RunID, 1, a.TaskIndex)
	if err != nil {
		return j, api.JobTaskResponse{}, jobresult.Manifest{}, err
	}
	if p.Limit != 1 || p.Offset != a.TaskIndex || len(p.Tasks) != 1 || p.Tasks[0].RunID != a.RunID || p.Tasks[0].TaskIndex != a.TaskIndex {
		return j, api.JobTaskResponse{}, jobresult.Manifest{}, invalid("invalid_response", "artifact task identity differs from the selected run")
	}
	task := p.Tasks[0]
	var manifest jobresult.Manifest
	if len(task.OutputManifest) != 0 && string(task.OutputManifest) != "null" {
		manifest, err = jobresult.Validate(task.OutputManifest)
		if err != nil {
			err = invalid("invalid_response", "task returned an invalid bounded output manifest")
		}
	}
	return j, task, manifest, err
}

func (m *management) listArtifacts(ctx context.Context, a jobTaskArgs) (any, error) {
	j, task, manifest, err := m.taskManifest(ctx, a)
	if err != nil {
		return nil, err
	}
	items := make([]jobArtifactView, 0, len(manifest.Artifacts))
	for _, artifact := range manifest.Artifacts {
		items = append(items, jobArtifactView{Name: artifact.Name, SizeBytes: artifact.SizeBytes,
			SHA256: artifact.SHA256, Managed: strings.HasPrefix(artifact.URI, "obj://")})
	}
	if err := m.recheckJob(ctx, j); err != nil {
		return nil, err
	}
	return map[string]any{"schema_version": 1, "job": a.Job, "run_id": a.RunID, "task_index": a.TaskIndex, "task_status": task.Status,
		"manifest_available": manifest.Version != 0, "items": items, "availability_verified": false}, nil
}

func validArtifactName(name string) bool {
	return name != "" && name != "." && name != ".." && len(name) <= 128 && utf8.ValidString(name) &&
		!strings.ContainsAny(name, "/\\") && !strings.ContainsFunc(name, unicode.IsControl)
}

func (m *management) readArtifact(ctx context.Context, a jobArtifactArgs) (any, error) {
	if !validArtifactName(a.Name) {
		return nil, invalid("invalid_argument", "name must be an exact artifact name without path separators or control characters")
	}
	j, _, manifest, err := m.taskManifest(ctx, jobTaskArgs{Job: a.Job, RunID: a.RunID, TaskIndex: a.TaskIndex})
	if err != nil {
		return nil, err
	}
	var selected *jobresult.Artifact
	for _, artifact := range manifest.Artifacts {
		if artifact.Name == a.Name {
			selected = &artifact
			break
		}
	}
	if selected == nil {
		return nil, invalid("artifact_not_found", "artifact is absent from this task's current output manifest")
	}
	if !strings.HasPrefix(selected.URI, "obj://") {
		return nil, invalid("unsupported_artifact", "only managed obj:// artifacts support native verified downloads")
	}
	out, err := m.client.DownloadJobArtifact(ctx, a.Job, a.RunID, a.TaskIndex, a.Name)
	if err != nil {
		return nil, err
	}
	if out.Name != selected.Name || out.SizeBytes != selected.SizeBytes || out.SHA256 != selected.SHA256 || !m.validArtifactDownload(out) {
		return nil, invalid("invalid_response", "artifact download differs from its declared identity, size, checksum or signed GET contract")
	}
	if err := m.recheckJob(ctx, j); err != nil {
		return nil, err
	}
	return map[string]any{"schema_version": 1, "job": a.Job, "run_id": a.RunID, "task_index": a.TaskIndex, "name": out.Name,
		"size_bytes": out.SizeBytes, "sha256": out.SHA256, "verified_at": out.VerifiedAt, "download": out.Download}, nil
}

func (m *management) validArtifactDownload(out api.JobArtifactDownloadResponse) bool {
	d := out.Download
	u, err := url.Parse(d.URL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" || len(d.URL) > 8192 ||
		d.Method != http.MethodGet || !d.ExpiresAt.After(time.Now()) || m.clean(d.URL) != d.URL {
		return false
	}
	if _, err := time.Parse(time.RFC3339Nano, out.VerifiedAt); err != nil {
		return false
	}
	for key, value := range d.Headers {
		switch strings.ToLower(key) {
		case "authorization", "cookie", "host", "proxy-authorization":
			return false
		}
		if strings.ContainsAny(key+value, "\r\n") || m.clean(value) != value {
			return false
		}
	}
	return true
}
