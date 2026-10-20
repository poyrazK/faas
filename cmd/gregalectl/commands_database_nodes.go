// commands_database_nodes.go — operator controls for native managed
// PostgreSQL database nodes (ADR-1027). A node's lifecycle (draining,
// unavailable) lives in the deployment manifest; moving or restarting one
// database is an authenticated, durable operator intent that schedd's
// database scheduler carries out.

package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

const dispatchDatabaseNodes = "database-nodes"

func cmdDatabaseNodes(args []string) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(osStderr, "gregalectl database-nodes: missing subcommand; want move, restart or switchover")
		return 2
	}
	switch args[0] {
	case "move":
		return cmdDatabaseNodesMove(args[1:])
	case "restart":
		return cmdDatabaseNodesRestart(args[1:])
	case "switchover":
		return cmdDatabaseNodesSwitchover(args[1:])
	default:
		_, _ = fmt.Fprintf(osStderr, "gregalectl database-nodes: unknown subcommand %q; want move, restart or switchover\n", args[0])
		return 2
	}
}

// cmdDatabaseNodesMove moves one native database to another database node.
// schedd parks it, records the move and copies its volume in the
// background: the intent succeeds once the move is recorded, and the
// database wakes on its new node when the copy is done.
func cmdDatabaseNodesMove(args []string) int {
	fs := flag.NewFlagSet("move", flag.ContinueOnError)
	fs.SetOutput(osStderr)
	database := fs.String("database", "", "the native database's id (its UUID in database_vms)")
	node := fs.String("node", "", "target database node; empty picks the active node with the most free volume capacity")
	reason := fs.String("reason", "", "audit reason ([a-z0-9_]{1,64}; defaults to operator_database_move)")
	timeout := fs.Duration("timeout", time.Minute, "maximum wait for the move to be recorded")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *database == "" {
		_, _ = fmt.Fprintln(osStderr, "gregalectl database-nodes move: --database required")
		return 2
	}
	return moveDatabaseViaAPI(*database, *node, *reason, *timeout)
}

func databaseMovePath(database, node, reason string) string {
	q := url.Values{"confirm": {"true"}}
	if node != "" {
		q.Set("node", node)
	}
	if reason != "" {
		q.Set("reason", reason)
	}
	return "/v1/admin/ops/databases/" + url.PathEscape(database) + "/move?" + q.Encode()
}

func moveDatabaseViaAPI(database, node, reason string, timeout time.Duration) int {
	if timeout <= 0 {
		_, _ = fmt.Fprintln(osStderr, "gregalectl database-nodes move: --timeout must be positive")
		return 2
	}
	sess, err := loadOperatorSession()
	if err != nil {
		_, _ = fmt.Fprintf(osStderr, "gregalectl database-nodes move: %v\n", err)
		return 1
	}
	client := newOperatorHTTPClient(&sess)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var accepted api.ObsDatabaseMoveResponse
	if err := client.doJSON(ctx, http.MethodPost, databaseMovePath(database, node, reason), nil, &accepted, true, nil); err != nil {
		_, _ = fmt.Fprintf(osStderr, "gregalectl database-nodes move: %v\n", err)
		return 1
	}
	outcome, code := awaitDatabaseIntent(ctx, client, "move", accepted.IntentID, accepted.StatusURL, timeout)
	if code != 0 {
		return code
	}
	if jsonEnabled() {
		return emitOperatorJSON(struct {
			Operation api.ObsDatabaseMoveResponse `json:"operation"`
			Outcome   api.OperatorIntentResponse  `json:"outcome"`
		}{accepted, outcome})
	}
	_, _ = fmt.Fprintf(osStdout, "move of %s recorded; intent=%s. schedd copies it in the background: watch schedd_database_moves_total, or its last_error (move_failed)\n", database, accepted.IntentID)
	return 0
}

// cmdDatabaseNodesRestart restarts one running native database in place
// onto its node's current base (a new PostgreSQL minor, guest-init or
// launch): schedd parks it and wakes it again, standby included. A parked
// database is left for its next wake, which boots the current base.
func cmdDatabaseNodesRestart(args []string) int {
	fs := flag.NewFlagSet("restart", flag.ContinueOnError)
	fs.SetOutput(osStderr)
	database := fs.String("database", "", "the native database's id (its UUID in database_vms)")
	reason := fs.String("reason", "", "audit reason ([a-z0-9_]{1,64}; defaults to operator_database_restart)")
	timeout := fs.Duration("timeout", 2*time.Minute, "maximum wait for the restart")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *database == "" {
		_, _ = fmt.Fprintln(osStderr, "gregalectl database-nodes restart: --database required")
		return 2
	}
	return restartDatabaseViaAPI(*database, *reason, *timeout)
}

func databaseRestartPath(database, reason string) string {
	q := url.Values{"confirm": {"true"}}
	if reason != "" {
		q.Set("reason", reason)
	}
	return "/v1/admin/ops/databases/" + url.PathEscape(database) + "/restart?" + q.Encode()
}

func restartDatabaseViaAPI(database, reason string, timeout time.Duration) int {
	if timeout <= 0 {
		_, _ = fmt.Fprintln(osStderr, "gregalectl database-nodes restart: --timeout must be positive")
		return 2
	}
	sess, err := loadOperatorSession()
	if err != nil {
		_, _ = fmt.Fprintf(osStderr, "gregalectl database-nodes restart: %v\n", err)
		return 1
	}
	client := newOperatorHTTPClient(&sess)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var accepted api.ObsDatabaseRestartResponse
	if err := client.doJSON(ctx, http.MethodPost, databaseRestartPath(database, reason), nil, &accepted, true, nil); err != nil {
		_, _ = fmt.Fprintf(osStderr, "gregalectl database-nodes restart: %v\n", err)
		return 1
	}
	outcome, code := awaitDatabaseIntent(ctx, client, "restart", accepted.IntentID, accepted.StatusURL, timeout)
	if code != 0 {
		return code
	}
	if jsonEnabled() {
		return emitOperatorJSON(struct {
			Operation api.ObsDatabaseRestartResponse `json:"operation"`
			Outcome   api.OperatorIntentResponse     `json:"outcome"`
		}{accepted, outcome})
	}
	_, _ = fmt.Fprintf(osStdout, "restarted %s onto its node's current base (a parked database boots it at its next wake); intent=%s\n", database, accepted.IntentID)
	return 0
}

// awaitDatabaseIntent polls an operator intent until it ends. It returns
// the outcome and 0 once it succeeded, else the exit code, having said why.
func awaitDatabaseIntent(ctx context.Context, client *operatorHTTPClient, verb, intentID, statusURL string, timeout time.Duration) (api.OperatorIntentResponse, int) {
	var outcome api.OperatorIntentResponse
	for {
		if err := client.doJSON(ctx, http.MethodGet, statusURL, nil, &outcome, false, nil); err != nil {
			_, _ = fmt.Fprintf(osStderr, "gregalectl database-nodes %s: poll intent %s: %v\n", verb, intentID, err)
			return outcome, 1
		}
		switch state.OperatorIntentStatus(outcome.Status) {
		case state.OperatorIntentSucceeded:
			return outcome, 0
		case state.OperatorIntentFailed, state.OperatorIntentCancelled:
			_, _ = fmt.Fprintf(osStderr, "gregalectl database-nodes %s: intent %s %s: %s\n", verb, intentID, outcome.Status, outcome.Error)
			return outcome, 1
		}
		select {
		case <-ctx.Done():
			_, _ = fmt.Fprintf(osStderr, "gregalectl database-nodes %s: intent %s did not finish before %s; schedd may not run the database scheduler\n", verb, intentID, timeout)
			return outcome, 1
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// cmdDatabaseNodesSwitchover hands a production-class native database to
// its synchronized standby on another node: schedd stops a running
// primary cleanly and promotes the standby, losing nothing, or moves a
// parked database to the standby's node, whose next boot promotes it.
func cmdDatabaseNodesSwitchover(args []string) int {
	fs := flag.NewFlagSet("switchover", flag.ContinueOnError)
	fs.SetOutput(osStderr)
	database := fs.String("database", "", "the native database's id (its UUID in database_vms)")
	reason := fs.String("reason", "", "audit reason ([a-z0-9_]{1,64}; defaults to operator_database_switchover)")
	timeout := fs.Duration("timeout", 2*time.Minute, "maximum wait for the switchover")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *database == "" {
		_, _ = fmt.Fprintln(osStderr, "gregalectl database-nodes switchover: --database required")
		return 2
	}
	return switchoverDatabaseViaAPI(*database, *reason, *timeout)
}

func databaseSwitchoverPath(database, reason string) string {
	q := url.Values{"confirm": {"true"}}
	if reason != "" {
		q.Set("reason", reason)
	}
	return "/v1/admin/ops/databases/" + url.PathEscape(database) + "/switchover?" + q.Encode()
}

func switchoverDatabaseViaAPI(database, reason string, timeout time.Duration) int {
	if timeout <= 0 {
		_, _ = fmt.Fprintln(osStderr, "gregalectl database-nodes switchover: --timeout must be positive")
		return 2
	}
	sess, err := loadOperatorSession()
	if err != nil {
		_, _ = fmt.Fprintf(osStderr, "gregalectl database-nodes switchover: %v\n", err)
		return 1
	}
	client := newOperatorHTTPClient(&sess)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var accepted api.ObsDatabaseSwitchoverResponse
	if err := client.doJSON(ctx, http.MethodPost, databaseSwitchoverPath(database, reason), nil, &accepted, true, nil); err != nil {
		_, _ = fmt.Fprintf(osStderr, "gregalectl database-nodes switchover: %v\n", err)
		return 1
	}
	outcome, code := awaitDatabaseIntent(ctx, client, "switchover", accepted.IntentID, accepted.StatusURL, timeout)
	if code != 0 {
		return code
	}
	if jsonEnabled() {
		return emitOperatorJSON(struct {
			Operation api.ObsDatabaseSwitchoverResponse `json:"operation"`
			Outcome   api.OperatorIntentResponse        `json:"outcome"`
		}{accepted, outcome})
	}
	_, _ = fmt.Fprintf(osStdout, "switched %s over to its standby (a parked database promotes it at its next wake); intent=%s\n", database, accepted.IntentID)
	return 0
}
