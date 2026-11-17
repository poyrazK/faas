package db

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// ownerClaimedChannels are delivered by the app's owning schedd: every schedd
// hears the broadcast and runs a replay worker, and only the owner serves the
// row. A row of these channels must open to replay only after a wake delay, or
// a peer's replay worker can claim it before the owner's live claim, which
// then finds the row leased and gives up (production 2026-11-16: a credential
// rotation's restart sat pending for hours).
var ownerClaimedChannels = []string{NotifyAppWake, NotifyRuntimeConfigRestart}

var (
	sqlQueryName     = regexp.MustCompile(`(?m)^-- name: (\w+)`)
	outboxWrite      = regexp.MustCompile(`insert into notification_outbox|update notification_outbox set[^;]*state\s*=\s*'pending'`)
	delayedAvailable = regexp.MustCompile(`available_at\s*(=\s*)?[^;]*(clock_timestamp|now)\(\)\s*\+`)
)

// TestOwnerClaimedOutboxRowsWaitForTheOwner reads every sqlc query file and
// requires each query that queues an app_wake or runtime_config_restart row
// to set available_at to a time after now. EnqueueDurableNotificationTx, the
// Go path, adds notificationOutboxWakeDelay itself.
func TestOwnerClaimedOutboxRowsWaitForTheOwner(t *testing.T) {
	checked := 0
	err := filepath.WalkDir("..", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".sql") || filepath.Base(path) == "schema.sql" {
			return err
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for name, query := range sqlQueries(string(body)) {
			if !queuesOwnerClaimedRow(query) {
				continue
			}
			checked++
			if !delayedAvailable.MatchString(query) {
				t.Errorf("%s: %s queues an owner-claimed notification that is open to replay at once; set available_at to clock_timestamp() plus a wake delay", path, name)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked == 0 {
		t.Fatal("found no query that queues an owner-claimed notification; the scan is broken")
	}
}

func TestOwnerClaimedOutboxGuardCatchesAnUndelayedRow(t *testing.T) {
	bad := "-- name: Bad :exec\nINSERT INTO notification_outbox(channel,payload) VALUES ('runtime_config_restart','{}');\n"
	good := "-- name: Good :exec\nINSERT INTO notification_outbox(channel,payload,available_at) VALUES ('app_wake','{}',clock_timestamp()+make_interval(secs=>5));\n"
	for src, want := range map[string]bool{bad: false, good: true} {
		for name, query := range sqlQueries(src) {
			if !queuesOwnerClaimedRow(query) || delayedAvailable.MatchString(query) != want {
				t.Errorf("%s: owner-claimed=%v delayed=%v, want delayed=%v", name, queuesOwnerClaimedRow(query), delayedAvailable.MatchString(query), want)
			}
		}
	}
}

// sqlQueries splits a sqlc query file into its named queries, lower-cased.
func sqlQueries(src string) map[string]string {
	out := map[string]string{}
	marks := sqlQueryName.FindAllStringSubmatchIndex(src, -1)
	for i, m := range marks {
		end := len(src)
		if i+1 < len(marks) {
			end = marks[i+1][0]
		}
		out[src[m[2]:m[3]]] = strings.ToLower(src[m[1]:end])
	}
	return out
}

func queuesOwnerClaimedRow(query string) bool {
	if !outboxWrite.MatchString(query) {
		return false
	}
	for _, channel := range ownerClaimedChannels {
		if strings.Contains(query, "'"+channel+"'") {
			return true
		}
	}
	return false
}
