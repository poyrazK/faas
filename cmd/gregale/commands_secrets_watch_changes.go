package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
)

func secretWatchRows(resp *api.AppSecretListResponse) map[string]api.AppSecretResponse {
	rows := map[string]api.AppSecretResponse{}
	add := func(row api.AppSecretResponse, scope string) {
		if api.ValidateSecretKey(row.Key) != nil {
			return
		}
		row.Scope = scopeOrDefault(scope)
		if api.ValidateScope(row.Scope) != nil {
			return
		}
		rows[row.Scope+"/"+row.Key] = row
	}
	if len(resp.SecretsByScope) > 0 {
		for scope, group := range resp.SecretsByScope {
			for _, row := range group {
				data, _ := json.Marshal(row)
				var flat api.AppSecretResponse
				if json.Unmarshal(data, &flat) == nil {
					add(flat, scope)
				}
			}
		}
	} else {
		for _, row := range resp.Secrets {
			add(row, row.Scope)
		}
	}
	return rows
}

func secretWatchRowFingerprint(row api.AppSecretResponse) []byte {
	data, _ := secretWatchFingerprint(api.AppSecretListResponse{Secrets: []api.AppSecretResponse{row}})
	return data
}

func secretWatchAcknowledgement(row api.AppSecretResponse) string {
	if len(row.RuntimeReloadObservations) == 0 {
		if row.RuntimeReloadTargetsComplete {
			return "no active authorized targets"
		}
		return "no reports; roster incomplete"
	}
	applied, failed, unconfirmed := 0, 0, 0
	for _, target := range row.RuntimeReloadObservations {
		if row.DeliveryVersion > 0 && target.ApplicationAckVersion == row.DeliveryVersion {
			switch target.ApplicationAck {
			case "applied":
				applied++
			case "failed":
				failed++
			default:
				unconfirmed++
			}
		} else {
			unconfirmed++
		}
	}
	completeness := "complete roster"
	if !row.RuntimeReloadTargetsComplete {
		completeness = "incomplete roster"
	}
	return fmt.Sprintf("%d applied, %d failed, %d unconfirmed (%s)", applied, failed, unconfirmed, completeness)
}

func secretWatchReloadFingerprint(row api.AppSecretResponse) []byte {
	row.DeliveryStatus = ""
	row.DeliveredVersion = 0
	row.DeliveryVersion = 0
	row.Key = ""
	row.Scope = ""
	row.SecretClass = ""
	row.LastDeliveryErrorCode = ""
	row.LastDeliveredWakeID = ""
	row.LastDeliveredInstanceID = ""
	targets := append([]api.SecretRuntimeReloadObservation(nil), row.RuntimeReloadObservations...)
	for i := range targets {
		targets[i].ApplicationAck = ""
		targets[i].ApplicationAckVersion = 0
		targets[i].ApplicationAckErrorCode = ""
		targets[i].ApplicationAckAt = ""
	}
	row.RuntimeReloadObservations = targets
	return secretWatchRowFingerprint(row)
}

func printSecretWatchChanges(before, after *api.AppSecretListResponse) {
	oldRows, newRows := secretWatchRows(before), secretWatchRows(after)
	identities := map[string]bool{}
	for key := range oldRows {
		identities[key] = true
	}
	for key := range newRows {
		identities[key] = true
	}
	keys := make([]string, 0, len(identities))
	for key := range identities {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	_, _ = fmt.Fprintln(osStdout, "Changed secret metadata:")
	changed := 0
	for _, key := range keys {
		old, hadOld := oldRows[key]
		next, hasNext := newRows[key]
		if hadOld && hasNext && bytes.Equal(secretWatchRowFingerprint(old), secretWatchRowFingerprint(next)) {
			continue
		}
		changed++
		if changed > 10 {
			continue
		}
		if !hadOld {
			_, _ = fmt.Fprintf(osStdout, "  %s: now appears in the selected view.\n", key)
			continue
		}
		if !hasNext {
			_, _ = fmt.Fprintf(osStdout, "  %s: no longer returned in the selected view; it may be absent or excluded by filters. This does not confirm deletion.\n", key)
			continue
		}
		_, _ = fmt.Fprintf(osStdout, "  %s:\n", key)
		detailed := false
		oldState := secretDeliveryState(old.DeliveryStatus, old.DeliveryVersion, old.DeliveredVersion)
		nextState := secretDeliveryState(next.DeliveryStatus, next.DeliveryVersion, next.DeliveredVersion)
		if oldState != nextState {
			_, _ = fmt.Fprintf(osStdout, "    Delivery: %s → %s\n", oldState, nextState)
			detailed = true
		}
		if old.DeliveryVersion != next.DeliveryVersion {
			_, _ = fmt.Fprintf(osStdout, "    Delivery version: %d → %d\n", old.DeliveryVersion, next.DeliveryVersion)
			detailed = true
		}
		oldAck, nextAck := secretWatchAcknowledgement(old), secretWatchAcknowledgement(next)
		if oldAck != nextAck {
			_, _ = fmt.Fprintf(osStdout, "    Application acknowledgement: %s → %s\n", oldAck, nextAck)
			detailed = true
		}
		if !bytes.Equal(secretWatchReloadFingerprint(old), secretWatchReloadFingerprint(next)) {
			_, _ = fmt.Fprintln(osStdout, "    Runtime reload or roster evidence changed; delivery and application acknowledgement remain separate.")
			detailed = true
		}
		if old.SecretClass != next.SecretClass {
			_, _ = fmt.Fprintf(osStdout, "    Retention class: %s → %s\n", compactInspectFailureText(secretClassLabel(old.SecretClass)), compactInspectFailureText(secretClassLabel(next.SecretClass)))
			detailed = true
		}
		if !detailed {
			_, _ = fmt.Fprintln(osStdout, "    Recorded metadata changed; review the current list for details.")
		}
	}
	if changed > 10 {
		_, _ = fmt.Fprintf(osStdout, "  %d more keys changed; review the current list for details.\n", changed-10)
	}
	if changed == 0 {
		_, _ = fmt.Fprintln(osStdout, "  Listed key evidence is unchanged; quota metadata or selected-key guidance changed.")
	}
	if before.Quota != after.Quota {
		_, _ = fmt.Fprintf(osStdout, "  Secret quota: %d → %d\n", before.Quota, after.Quota)
	}
}

func emitSecretWatchChangesHuman(app, scope, class, olderThan, delivery string, before, after *api.AppSecretListResponse, hints []secretKeyHint, keys []string) error {
	original := osStdout
	writer := &secretWatchWriter{target: original}
	osStdout = writer
	defer func() { osStdout = original }()
	printSecretWatchChanges(before, after)
	if secretListHasRows(after) {
		printSecretDeliveryOverview(app, scope, after)
	} else {
		printEmptySecretList(app, scope, class, olderThan, delivery, keys...)
	}
	printSecretKeyHints(app, scope, hints)
	command := fmt.Sprintf("%s secrets list --app %s --scope %s", recoveryCLI(), recoveryArgument(app), recoveryArgument(scopeOrDefault(scope)))
	for _, key := range keys {
		command += " --key " + recoveryArgument(key)
	}
	if class != "" {
		command += " --class " + recoveryArgument(class)
	}
	if olderThan != "" {
		command += " --older-than " + recoveryArgument(olderThan)
	}
	if delivery != "" {
		command += " --delivery-status " + recoveryArgument(delivery)
	}
	_, _ = fmt.Fprintf(osStdout, "Review current rows (POSIX shells):\n  %s\n", command)
	return writer.err
}
