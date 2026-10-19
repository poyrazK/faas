//go:build linux

package objectqualification

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectnode"
)

func (l *lab) bootstrapFollower(ctx context.Context, seed string) (objectnode.ReplicationStatus, error) {
	if l.options.Remote.Host == "" {
		if _, err := objectnode.BootstrapReplica(ctx, seed, l.followerDir); err != nil {
			return objectnode.ReplicationStatus{}, err
		}
		return objectnode.ReadReplicationStatus(l.followerDir)
	}
	var err error
	l.remote, err = l.connectRemote(ctx)
	if err != nil {
		return objectnode.ReplicationStatus{}, err
	}
	if err = l.remote.uploadBackup(ctx, seed); err != nil {
		return objectnode.ReplicationStatus{}, err
	}
	result, err := l.remote.call(ctx, agentRequest{Operation: "bootstrap"})
	return result.Status, err
}

func (l *lab) compactFollower(ctx context.Context) error {
	if l.remote != nil && !l.remotePrimary.Load() {
		_, err := l.remote.call(ctx, agentRequest{Operation: "compact"})
		return err
	}
	_, err := objectnode.CompactJournal(ctx, l.followerDir, -1)
	return err
}

func (l *lab) promoteRemote(ctx context.Context) error {
	for _, operation := range []string{"fence", "catch-up", "promote"} {
		for {
			result, err := l.remote.call(ctx, agentRequest{Operation: operation, Source: l.remote.source})
			if err != nil {
				return err
			}
			if operation != "catch-up" || result.Done {
				break
			}
		}
	}
	if err := l.stopPrimary(ctx, false); err != nil {
		return err
	}
	l.remotePrimary.Store(true)
	l.followerDir = l.newPath("successor-follower")
	seed := l.newPath("successor-seed")
	if err := l.remote.downloadBackup(ctx, seed); err != nil {
		return err
	}
	_, err := objectnode.BootstrapReplica(ctx, seed, l.followerDir)
	if err != nil {
		return err
	}
	status, err := objectnode.ReadReplicationStatus(l.followerDir)
	if err != nil {
		return err
	}
	if _, err = l.remote.call(ctx, agentRequest{Operation: "register", Status: status, Mode: l.mode}); err != nil {
		return err
	}
	if err = l.startPrimary(ctx, api.ObjectUploadSpoolMinFreeBytes); err != nil {
		return err
	}
	if err = l.startFollower(ctx); err != nil {
		return err
	}
	return l.caughtUp(ctx)
}
