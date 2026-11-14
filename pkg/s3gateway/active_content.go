package s3gateway

import (
	"mime"
	"net/http"

	"github.com/onebox-faas/faas/pkg/objectstorage"
)

// activeObjectSandboxCSP keeps an object a browser opens on the gateway's
// shared origin from running script, submitting forms or reading the origin.
const activeObjectSandboxCSP = "sandbox; default-src 'none'; style-src 'unsafe-inline'; img-src data:"

// sandboxObjectResponse stops one tenant's objects acting as pages on the
// gateway host, which every tenant shares (s3.gregale.dev; object storage
// audit OS-9, 2026-11-10). Gregale-issued signed URLs were already downloads
// (setURLDownloadHeaders); a GET signed with a customer's own S3 key, or a
// response-content-type override, still returned the stored type, so an
// uploaded HTML or SVG page ran script on a first-party Gregale host.
//
// Every object response gets nosniff and a type. An active type (HTML, SVG,
// XML, script; objectstorage.ActiveContentType) also gets a sandbox CSP and is
// served as an attachment, keeping any filename it was stored with. Bucket
// owners who host pages use a public bucket on their app's own hostname.
func sandboxObjectResponse(headers http.Header) {
	headers.Set("X-Content-Type-Options", "nosniff")
	contentType := headers.Get("Content-Type")
	if contentType == "" {
		headers.Set("Content-Type", "application/octet-stream")
		return
	}
	if !objectstorage.ActiveContentType(contentType) {
		if _, _, err := mime.ParseMediaType(contentType); err == nil {
			return
		}
		// A type browsers may not parse the way we do is not trusted as inert.
	}
	headers.Set("Content-Security-Policy", activeObjectSandboxCSP)
	headers.Set("Content-Disposition", attachmentDisposition(headers.Get("Content-Disposition")))
}

// attachmentDisposition turns a stored disposition into an attachment,
// keeping its parameters (a filename) when they parse.
func attachmentDisposition(stored string) string {
	if stored == "" {
		return "attachment"
	}
	_, params, err := mime.ParseMediaType(stored)
	if err != nil || len(params) == 0 {
		return "attachment"
	}
	if formatted := mime.FormatMediaType("attachment", params); formatted != "" {
		return formatted
	}
	return "attachment"
}
