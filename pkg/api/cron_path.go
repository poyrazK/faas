package api

import (
	"fmt"
	"net/http"
	"regexp"
)

// cronPathPattern accepts an origin-relative request path on the cron's own
// app: "/" or "/x…", never "//host", and no whitespace, control characters,
// backslashes or fragment. A query string is allowed.
var cronPathPattern = regexp.MustCompile(`^/([^/\s\\#\x00-\x1f\x7f][^\s\\#\x00-\x1f\x7f]*)?$`)

// ErrCronNotATrigger is the 400 for GET /v1/triggers?kind=cron. Cron
// schedules are listed and edited through /v1/crons, never as triggers
// (issue #2757).
func ErrCronNotATrigger() *Problem {
	return ErrValidation("cron schedules are not triggers; list them with GET /v1/crons (gregale crons list)").
		WithDocs(docsBase + "/crons")
}

// ValidateCronPath returns a 400 problem when path is not a request path an
// HTTP cron may send. Callers apply the "/" default before validating, so an
// empty path is rejected.
func ValidateCronPath(path string) *Problem {
	if len(path) > CronPathMaxBytes {
		return NewProblem(http.StatusBadRequest, CodeCronInvalid, "Invalid cron path",
			fmt.Sprintf("path is %d bytes; a cron path is at most %d bytes", len(path), CronPathMaxBytes)).
			WithByteLimit(CronPathMaxBytes, int64(len(path))).
			WithDocs(docsBase + "/crons")
	}
	if !cronPathPattern.MatchString(path) {
		return NewProblem(http.StatusBadRequest, CodeCronInvalid, "Invalid cron path",
			"path must start with a single / and contain no whitespace, control characters, backslashes or #; a cron always calls its own app").
			WithDocs(docsBase + "/crons")
	}
	return nil
}
