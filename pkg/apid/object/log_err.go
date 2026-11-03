package object

import (
	"errors"
	"net/url"
	"strings"
)

// logErr is err as a background sweep logs it. A url.Error names the request
// URL, and a presigned one carries its signature in the query string, so the
// query is dropped; everything else is kept so the log says why the sweep
// failed.
func logErr(err error) string {
	if err == nil {
		return ""
	}
	text := err.Error()
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		if u, parseErr := url.Parse(urlErr.URL); parseErr == nil && (u.RawQuery != "" || u.User != nil) {
			u.RawQuery, u.User = "", nil
			text = strings.ReplaceAll(text, urlErr.URL, u.String())
		} else if parseErr != nil {
			text = strings.ReplaceAll(text, urlErr.URL, "<url>")
		}
	}
	return text
}
