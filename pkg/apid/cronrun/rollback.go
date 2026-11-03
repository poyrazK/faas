package cronrun

import (
	"fmt"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// rollbackNoticePaths bounds how many cron paths a rollback notice names.
const rollbackNoticePaths = 3

// RollbackNotice is the warning for a rollback to a deployment created at
// since, or "" when no running HTTP cron was added after it. Crons are app
// settings, not part of a release: a rollback leaves the crons a later release
// declared, and they keep calling paths the older code may not serve, failing
// with 404 on every scheduled time (crons audit 2026-11-02).
func RollbackNotice(crons []state.Cron, since time.Time) string {
	var paths []string
	added := 0
	for _, c := range crons {
		if len(c.Command) > 0 || !c.Enabled || c.SuspendedReason != "" || !c.CreatedAt.After(since) {
			continue
		}
		added++
		if len(paths) < rollbackNoticePaths {
			// The notice travels in a quoted Warning header value.
			paths = append(paths, "POST "+strings.ReplaceAll(c.Path, `"`, ""))
		}
	}
	if added == 0 {
		return ""
	}
	if added > len(paths) {
		paths = append(paths, fmt.Sprintf("and %d more", added-len(paths)))
	}
	return fmt.Sprintf("crons are not rolled back: %d cron(s) added after this deployment keep running (%s); "+
		"if it does not serve those paths, update or delete them with gregale cron", added, strings.Join(paths, ", "))
}
