package api

import (
	"bytes"
	"mime/multipart"
)

// newMultipartWriter builds the multipart/form-data writer used by
// DeployMultipart. The slug field is shipped for apid's optional
// path-validator (the URL path is the source of truth — see cmd/apid/handlers.go
// createDeployment), but the server actually keys off the {slug} URL
// component. The dockerfile flag gates function-runnner vs Dockerfile
// builds (apid/dispatch).
func newMultipartWriter(dst *bytes.Buffer, slug string, dockerfile bool, runtime, handler string) *multipart.Writer {
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
	return w
}
