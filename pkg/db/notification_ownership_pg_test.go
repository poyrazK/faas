// adr: 462 — only the owner claims node-local handoffs; skips spend no retries.
package db

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestNodeScopedClaimsSkipSiblingBacklogAndKeepLegacyWork(t *testing.T) {
	pool, ctx := notificationOutboxPG(t)
	peerPayload := nodeBootPayload(t, "node-b")
	if _, err := pool.Exec(ctx, "INSERT INTO notification_outbox (channel, payload) SELECT $1, $2 FROM generate_series(1, 64)", NotifySnapshotBoot, peerPayload); err != nil {
		t.Fatal(err)
	}
	// Oldest sibling rows must not hide local work beyond the replay batch.
	for _, tc := range []struct{ channel, payload string }{
		{NotifySnapshotBoot, nodeBootPayload(t, "node-a")},
		{NotifySnapshotBoot, nodeBootPayload(t, "")},
		{NotifySnapshotWritten, peerPayload},
	} {
		var id int64
		if err := pool.QueryRow(ctx, "INSERT INTO notification_outbox (channel, payload) VALUES ($1, $2) RETURNING id", tc.channel, tc.payload).Scan(&id); err != nil {
			t.Fatal(err)
		}
		item, err := ClaimNotificationForNode(ctx, pool, "imaged", " node-a ", []string{NotifySnapshotBoot, NotifySnapshotWritten}, time.Minute)
		if err != nil || item.ID != id || item.Attempts != 1 {
			t.Fatalf("local/shared claim = %+v err=%v, want id %d", item, err, id)
		}
		if err := CompleteNotification(ctx, pool, id, item.ClaimToken); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := ClaimNotificationForNode(ctx, pool, "imaged", "node-a", []string{NotifySnapshotBoot}, time.Minute); !errors.Is(err, ErrNotificationOutboxEmpty) {
		t.Fatalf("sibling backlog became claimable: %v", err)
	}
	var untouched int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM notification_outbox WHERE payload=$1 AND channel=$2 AND state='pending' AND attempts=0 AND claimed_by IS NULL", peerPayload, NotifySnapshotBoot).Scan(&untouched); err != nil || untouched != 64 {
		t.Fatalf("sibling backlog changed: count=%d err=%v", untouched, err)
	}
	// An unnamed single-box consumer keeps the existing compatibility path.
	item, err := ClaimNotification(ctx, pool, "imaged", []string{NotifySnapshotBoot}, time.Minute)
	if err != nil || item.Payload != peerPayload || item.Attempts != 1 {
		t.Fatalf("unnamed legacy claim = %+v err=%v", item, err)
	}
}

func TestNotificationTargetNodeHandlesWhitespaceAndMalformedPayloads(t *testing.T) {
	pool, ctx := notificationOutboxPG(t)
	spaces := " \t\n\v\f\r\u0085\u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000"
	for _, space := range spaces {
		raw := string(space) + "vega-v" + string(space)
		var node string
		if err := pool.QueryRow(ctx, "SELECT notification_outbox_target_node($1, $2)", NotifySnapshotBoot, nodeBootPayload(t, raw)).Scan(&node); err != nil || node != strings.TrimSpace(raw) || !NotificationMatchesNode(node, raw) {
			t.Fatalf("whitespace U+%04X: node=%q err=%v", space, node, err)
		}
	}
	for _, payload := range []string{
		"{not-json", "null", "[]", "{}", "{\"node_id\":null}", "{\"node_id\":123}", "{\"node_id\":\"\\u0000\"}",
	} {
		t.Run(payload, func(t *testing.T) {
			var node string
			if err := pool.QueryRow(ctx, "SELECT notification_outbox_target_node($1, $2)", NotifySnapshotBoot, payload).Scan(&node); err != nil || node != "" {
				t.Fatalf("malformed/legacy target = %q err=%v", node, err)
			}
		})
	}
	var badID, goodID int64
	if err := pool.QueryRow(ctx, "INSERT INTO notification_outbox (channel, payload) VALUES ($1, $2) RETURNING id", NotifySnapshotBoot, "{not-json").Scan(&badID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "INSERT INTO notification_outbox (channel, payload) VALUES ($1, $2) RETURNING id", NotifySnapshotBoot, nodeBootPayload(t, "node-a")).Scan(&goodID); err != nil {
		t.Fatal(err)
	}
	seen := []int64{}
	delivered, err := DrainNotificationOutboxOnceForNode(ctx, pool, "imaged", "node-a", []string{NotifySnapshotBoot}, func(_ context.Context, n Notification) error {
		seen = append(seen, n.OutboxID)
		if n.OutboxID == badID {
			return errors.New("malformed snapshot_boot")
		}
		return nil
	}, nil)
	if err != nil || delivered != 1 || len(seen) != 2 || seen[0] != badID || seen[1] != goodID {
		t.Fatalf("bad payload blocked healthy work: delivered=%d seen=%v err=%v", delivered, seen, err)
	}
	var state string
	var attempts int
	if err := pool.QueryRow(ctx, "SELECT state, attempts FROM notification_outbox WHERE id=$1", badID).Scan(&state, &attempts); err != nil || state != "pending" || attempts != 1 {
		t.Fatalf("malformed delivery lost retry semantics: state=%s attempts=%d err=%v", state, attempts, err)
	}
}

func TestUnownedDeliveryReleasesClaimWithoutSpendingAttempt(t *testing.T) {
	pool, ctx := notificationOutboxPG(t)
	var id int64
	if err := pool.QueryRow(ctx, "INSERT INTO notification_outbox (channel, payload, attempts, last_error) VALUES ($1, $2, 2, 'earlier failure') RETURNING id", NotifySnapshotBoot, nodeBootPayload(t, "node-b")).Scan(&id); err != nil {
		t.Fatal(err)
	}
	calls := 0
	delivered, err := DrainNotificationOutboxOnce(ctx, pool, "imaged", []string{NotifySnapshotBoot}, func(context.Context, Notification) error {
		calls++
		return fmt.Errorf("sibling daemon: %w", ErrNotificationNotOwned)
	}, nil)
	if err != nil || delivered != 0 || calls != 1 {
		t.Fatalf("skip = delivered %d calls %d err=%v", delivered, calls, err)
	}
	var state, message string
	var attempts int
	var unleased bool
	if err := pool.QueryRow(ctx, "SELECT state, attempts, last_error, claimed_by IS NULL AND lease_until IS NULL AND delivered_at IS NULL FROM notification_outbox WHERE id=$1", id).Scan(&state, &attempts, &message, &unleased); err != nil || state != "pending" || attempts != 2 || message != "earlier failure" || !unleased {
		t.Fatalf("skip spent retry or changed outcome: state=%s attempts=%d message=%s unleased=%v err=%v", state, attempts, message, unleased, err)
	}
	// A token-fenced completion that wins before a stale skip cannot be reopened.
	item, err := ClaimNotification(ctx, pool, "imaged", []string{NotifySnapshotBoot}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := CompleteNotification(ctx, pool, item.ID, item.ClaimToken); err != nil {
		t.Fatal(err)
	}
	if rows, err := sqlc.New().ReleaseUnownedNotification(ctx, pool, sqlc.ReleaseUnownedNotificationParams{ID: item.ID, ClaimToken: item.ClaimToken}); err != nil || rows != 0 {
		t.Fatalf("stale skip after completion: rows=%d err=%v", rows, err)
	}
	if err := pool.QueryRow(ctx, "SELECT state FROM notification_outbox WHERE id=$1", id).Scan(&state); err != nil || state != "delivered" {
		t.Fatalf("stale skip reopened completed work: state=%s err=%v", state, err)
	}
}

func TestNodeScopedClaimsAcceptOversizedOwnerWithoutQueuePoisoning(t *testing.T) {
	pool, ctx := notificationOutboxPG(t)
	var owner strings.Builder
	// High entropy prevents index compression from hiding a key-size failure.
	for i := range 96 {
		_, _ = fmt.Fprintf(&owner, "%x", sha256.Sum256([]byte(fmt.Sprint(i))))
	}
	largeNode := owner.String()
	if _, err := pool.Exec(ctx, "INSERT INTO notification_outbox (channel, payload) VALUES ($1, $2)", NotifySnapshotBoot, nodeBootPayload(t, largeNode)); err != nil {
		t.Fatalf("oversized owner poisoned queue writes: %v", err)
	}
	var localID int64
	if err := pool.QueryRow(ctx, "INSERT INTO notification_outbox (channel, payload) VALUES ($1, $2) RETURNING id", NotifySnapshotBoot, nodeBootPayload(t, "node-a")).Scan(&localID); err != nil {
		t.Fatal(err)
	}
	item, err := ClaimNotificationForNode(ctx, pool, "imaged", "node-a", []string{NotifySnapshotBoot}, time.Minute)
	if err != nil || item.ID != localID {
		t.Fatalf("oversized peer blocked local work: id=%d err=%v", item.ID, err)
	}
	if err := CompleteNotification(ctx, pool, item.ID, item.ClaimToken); err != nil {
		t.Fatal(err)
	}
	item, err = ClaimNotificationForNode(ctx, pool, "imaged", largeNode, []string{NotifySnapshotBoot}, time.Minute)
	if err != nil || item.ID == localID {
		t.Fatalf("full owner identity stopped matching: id=%d err=%v", item.ID, err)
	}
}

func TestNodeScopedLeaseReclaimFencesStaleSkips(t *testing.T) {
	pool, ctx := notificationOutboxPG(t)
	var ownID, peerID int64
	for _, tc := range []struct {
		node string
		id   *int64
	}{{"node-b", &peerID}, {"node-a", &ownID}} {
		if err := pool.QueryRow(ctx, "INSERT INTO notification_outbox (channel, payload, state, attempts, claimed_by, lease_until) VALUES ($1, $2, 'processing', 2, 'stale', now()-interval '1 second') RETURNING id", NotifySnapshotBoot, nodeBootPayload(t, tc.node)).Scan(tc.id); err != nil {
			t.Fatal(err)
		}
	}
	item, err := ClaimNotificationForNode(ctx, pool, "imaged", "node-a", []string{NotifySnapshotBoot}, time.Minute)
	if err != nil || item.ID != ownID || item.Attempts != 3 {
		t.Fatalf("owner recovery = %+v err=%v", item, err)
	}
	if _, err := sqlc.New().ReleaseUnownedNotification(ctx, pool, sqlc.ReleaseUnownedNotificationParams{ID: ownID, ClaimToken: "stale"}); err != nil {
		t.Fatal(err)
	}
	var token string
	var attempts int
	if err := pool.QueryRow(ctx, "SELECT claimed_by, attempts FROM notification_outbox WHERE id=$1", ownID).Scan(&token, &attempts); err != nil || token != item.ClaimToken || attempts != 3 {
		t.Fatalf("stale skip changed owner lease: token=%s attempts=%d err=%v", token, attempts, err)
	}
	if err := CompleteNotification(ctx, pool, ownID, item.ClaimToken); err != nil {
		t.Fatal(err)
	}
	item, err = ClaimNotificationForNode(ctx, pool, "restarted-imaged", "node-b", []string{NotifySnapshotBoot}, time.Minute)
	if err != nil || item.ID != peerID || item.Attempts != 3 {
		t.Fatalf("sibling owner recovery = %+v err=%v", item, err)
	}
}

func TestTwoNodeConsumersDeliverOnlyTheirOwnHandoffs(t *testing.T) {
	pool, ctx := notificationOutboxPG(t)
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for _, node := range []string{"node-a", "node-b"} {
		if _, err := pool.Exec(ctx, "INSERT INTO notification_outbox (channel, payload) VALUES ($1, $2)", NotifySnapshotBoot, nodeBootPayload(t, node)); err != nil {
			t.Fatal(err)
		}
	}
	entered := make(chan string, 2)
	release := make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	results := make(chan error, 2)
	for _, node := range []string{"node-a", "node-b"} {
		go func() {
			delivered, err := DrainNotificationOutboxOnceForNode(ctx, pool, "imaged", node, []string{NotifySnapshotBoot}, func(ctx context.Context, n Notification) error {
				var body map[string]string
				if err := json.Unmarshal([]byte(n.Payload), &body); err != nil {
					return err
				}
				if body["node_id"] != node {
					return fmt.Errorf("node %s received owner %s", node, body["node_id"])
				}
				entered <- node
				select {
				case <-release:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			}, nil)
			if err == nil && delivered != 1 {
				err = fmt.Errorf("node %s delivered %d rows", node, delivered)
			}
			results <- err
		}()
	}
	owners := map[string]bool{}
	for range 2 {
		select {
		case node := <-entered:
			owners[node] = true
		case err := <-results:
			t.Fatalf("consumer ended before concurrent handling: %v", err)
		case <-ctx.Done():
			t.Fatal("two owners did not claim concurrently")
		}
	}
	if len(owners) != 2 {
		t.Fatalf("two consumers claimed the same owner: %v", owners)
	}
	close(release)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	var delivered int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM notification_outbox WHERE state='delivered' AND attempts=1").Scan(&delivered); err != nil || delivered != 2 {
		t.Fatalf("concurrent delivery = %d rows err=%v", delivered, err)
	}
}

func nodeBootPayload(t *testing.T, node string) string {
	t.Helper()
	raw, err := json.Marshal(map[string]string{"deployment_id": "deployment", "node_id": node})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
