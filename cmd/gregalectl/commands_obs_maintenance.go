package main

import (
	"fmt"
	"io"
)

func writeObsMaintenanceHuman(w io.Writer, snap map[string]any) {
	history, ok := snap["alert_history"].(map[string]any)
	if !ok {
		_, _ = fmt.Fprintln(w, "alert_collection: unknown (metadata unavailable)")
	} else {
		state, ok := history["collection_state"].(string)
		if !ok || state == "" {
			state = "unknown (legacy metadata)"
		}
		_, _ = fmt.Fprintf(w, "alert_collection: %s reason=%v last_attempt_at=%v last_success_at=%v\n", state, history["reason"], history["last_attempt_at"], history["last_success_at"])
	}
	retention, ok := snap["alert_retention"].(map[string]any)
	if !ok {
		_, _ = fmt.Fprintln(w, "alert_retention: unknown (metadata unavailable)")
		return
	}
	_, _ = fmt.Fprintf(w, "alert_retention: %v reason=%v as_of=%v cutoff=%v last_attempt_at=%v last_success_at=%v last_pruned_episodes=%v expired_episodes=%v oldest_expired_at=%v\n",
		retention["state"], retention["reason"], retention["as_of"], retention["cutoff"], retention["last_attempt_at"], retention["last_success_at"], retention["last_pruned_episodes"], retention["expired_episodes"], retention["oldest_expired_at"])
}
