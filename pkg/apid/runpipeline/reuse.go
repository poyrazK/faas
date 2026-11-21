package runpipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func reusedStep(r *api.ExecutionPipelineRerun, label string) *api.ExecutionPipelineReusedStep {
	if r != nil {
		for i := range r.Reused {
			if r.Reused[i].Label == label {
				return &r.Reused[i]
			}
		}
	}
	return nil
}
func outputDigest(r state.Execution) string {
	refs := []struct {
		Name   string
		Size   int
		SHA256 string
		FileID string
	}{}
	for _, a := range r.Artifacts {
		refs = append(refs, struct {
			Name   string
			Size   int
			SHA256 string
			FileID string
		}{a.Name, a.SizeBytes, a.SHA256, a.FileID})
	}
	raw, _ := json.Marshal(struct {
		Result    json.RawMessage
		Artifacts any
	}{r.Result, refs})
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:])
}
func (w Worker) reusedRun(ctx context.Context, j state.ExecutionWorkflowJob, ref api.ExecutionPipelineReusedStep) (state.Execution, error) {
	r, e := w.Runs.ExecutionByID(ctx, j.AccountID, ref.RunID)
	if e != nil {
		return r, e
	}
	if r.RunsPrincipalID == nil || j.RunsPrincipalID == nil || *r.RunsPrincipalID != *j.RunsPrincipalID || r.FinishedAt == nil || !w.now().Before(r.FinishedAt.Add(api.ExecutionPipelineReuseRetention)) || r.Status != api.ExecutionStatusSucceeded || r.ResultValidation != nil && r.ResultValidation.Status == "invalid" || outputDigest(r) != ref.OutputSHA256 {
		return r, fmt.Errorf("reused receipt unavailable")
	}
	if e = api.ValidateExecutionArtifacts(r.Artifacts); e != nil {
		return r, e
	}
	for _, a := range r.Artifacts {
		if a.FileID != "" {
			files, ok := w.Runs.(state.ExecutionFileAdmissionReader)
			if !ok {
				return r, fmt.Errorf("file metadata unavailable")
			}
			f, e := files.ExecutionFileByID(ctx, j.AccountID, a.FileID)
			if e != nil {
				return r, e
			}
			if f.Status != "ready" || !f.ExpiresAt.After(w.now()) || f.PrincipalID == nil || *f.PrincipalID != *j.RunsPrincipalID || f.ExecutionID == nil || *f.ExecutionID != r.ID || f.SizeBytes != int64(a.SizeBytes) || f.SHA256 != a.SHA256 {
				return r, fmt.Errorf("reused artifact unavailable")
			}
		}
	}
	return r, nil
}
