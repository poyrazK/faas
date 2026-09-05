package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"

	"github.com/onebox-faas/faas/pkg/sourcecontext"
)

// newMultipartWriterWithSourceRoot builds the multipart/form-data writer used
// by DeployMultipartWithSourceRoot. The slug field is shipped for apid's
// optional path-validator (the URL path is the source of truth — see
// cmd/apid/handlers.go createDeployment), but the server actually keys off
// the {slug} URL component. The dockerfile flag gates function-runner vs
// Dockerfile builds (apid/dispatch).
//
// Annotation fields (issue #977 / ADR-116): when an annotation is
// non-zero on `a`, the corresponding multipart form field is emitted
// (reason / tag / deployed_by / pr_number). nil/zero values skip the
// field entirely — the server defaults them to NULL on the row.
func newMultipartWriterWithSourceRoot(dst *bytes.Buffer, slug string, dockerfile bool, runtime, handler, sourceRoot string, a DeployAnnotations) *multipart.Writer {
	w := multipart.NewWriter(dst)
	// slug is redundant (URL has it too) but apid accepts it for log
	// clarity. Don't error if the writer fails — the caller checks
	// via err on Close/CreateFormFile.
	_ = w.WriteField("slug", slug)
	if dockerfile {
		_ = w.WriteField("dockerfile", "true")
	}
	if runtime != "" {
		_ = w.WriteField("runtime", runtime)
	}
	if handler != "" {
		_ = w.WriteField("handler", handler)
	}
	if sourceRoot != "" {
		_ = w.WriteField("source_root", sourceRoot)
	}
	if a.Reason != "" {
		_ = w.WriteField("reason", a.Reason)
	}
	if a.Tag != "" {
		_ = w.WriteField("tag", a.Tag)
	}
	if a.DeployedBy != "" {
		_ = w.WriteField("deployed_by", a.DeployedBy)
	}
	if a.PRNumber > 0 {
		_ = w.WriteField("pr_number", fmt.Sprintf("%d", a.PRNumber))
	}
	if len(a.Workflows) > 0 {
		if raw, err := json.Marshal(a.Workflows); err == nil {
			_ = w.WriteField("workflows", string(raw))
		}
	}
	return w
}

// DeployAnnotations is the annotation surface shared by every
// CLI-driven deploy path (issue #977 / ADR-116). Zero values mean
// "no annotation"; the server treats them as NULL on the row.
// Pointer fields are intentionally avoided so the multipart writer
// (which has no notion of nil) stays a single seam; nil-vs-zero is
// re-derived from the column scan via the coalesce-on-read pattern
// at pkg/state/pgstore.go.
type DeployAnnotations struct {
	Reason     string // free text, ≤280 chars (DB CHECK)
	Tag        string // closed-set enum (DB CHECK; handler validates too)
	DeployedBy string // human-readable actor label
	PRNumber   int    // positive int (DB CHECK; 0 collapses to NULL)
	Workflows  []WorkflowSpec
}

func normalizeMultipartSourceRoot(raw string) (string, error) {
	return sourcecontext.StorageRoot(raw)
}
