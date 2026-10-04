package db

import (
	"errors"
	"strings"
)

// ErrNotificationNotOwned means the daemon skipped a node-local handoff.
// Receiving it is neither successful handling nor a delivery failure.
var ErrNotificationNotOwned = errors.New("db: notification belongs to another node")

// NotificationMatchesNode preserves unnamed single-box daemons and legacy
// events without an owner. Named snapshot_boot deliveries require a match.
// The SQL resolver uses the same Unicode whitespace normalization.
func NotificationMatchesNode(localNode, ownerNode string) bool {
	localNode, ownerNode = strings.TrimSpace(localNode), strings.TrimSpace(ownerNode)
	return localNode == "" || ownerNode == "" || localNode == ownerNode
}
