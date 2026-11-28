package grace

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

// ErrPurgeNotExpired is PurgeApp's answer for an app that is not a deleted
// tombstone whose restore window has ended.
var ErrPurgeNotExpired = errors.New("grace: app is not a deleted app past its restore window")

// errArtifactBackendMissing refuses a purge that would orphan stored artifacts.
var errArtifactBackendMissing = errors.New("grace: artifact backend unavailable")

// PurgeApp permanently removes one deleted app whose restore window has
// already ended (state.Store.EndAppDeleteGrace), on the sweep's own path, so
// the slug is free at once instead of on a later tick. An error means the
// tombstone is still there; the sweep keeps retrying it.
func (g *Grace) PurgeApp(ctx context.Context, appID, actor string) error {
	app, err := g.p.Store.AppByID(ctx, appID)
	if err != nil {
		return err
	}
	now := g.p.Now()
	if app.Status != state.AppDeleted || app.DeleteGraceUntil == nil || app.DeleteGraceUntil.After(now) {
		return ErrPurgeNotExpired
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	_, err = g.purgeExpired(ctx, app, now, actor)
	return err
}

func artifactBytes(artifacts []state.AppDeletionArtifact) int64 {
	var total int64
	for _, artifact := range artifacts {
		if artifact.Bytes > 0 {
			total += artifact.Bytes
		}
	}
	return total
}

// purgeExpired claims an expired tombstone, deletes its storage artifacts and
// then its rows. It returns the artifact bytes left behind and, while the
// tombstone remains, why. Caller holds g.mu.
func (g *Grace) purgeExpired(ctx context.Context, app state.App, now time.Time, actor string) (int64, error) {
	if err := g.p.Store.ClaimAppDeletion(ctx, app.ID); err != nil {
		if !errors.Is(err, state.ErrNotFound) {
			g.metrics.failure("claim_tombstone")
			g.p.Log.Warn("grace: claim app deletion failed", "app", app.ID, "err", err)
		}
		return 0, err
	}
	artifacts, err := g.p.Store.ListAppDeletionArtifacts(ctx, app.ID)
	if err != nil {
		g.metrics.failure("list_artifacts")
		g.p.Log.Warn("grace: list app deletion artifacts failed", "app", app.ID, "err", err)
		return 0, err
	}
	if len(artifacts) != 0 && g.p.Artifacts == nil {
		retained := artifactBytes(artifacts)
		g.metrics.failure("artifact_backend_missing")
		g.p.Log.Error("grace: refusing app purge without artifact backend",
			"app", app.ID, "artifact_count", len(artifacts), "artifact_bytes", retained)
		return retained, errArtifactBackendMissing
	}
	var retained int64
	var artifactErr error
	for _, artifact := range artifacts {
		if err := g.p.Artifacts.Delete(ctx, artifact.Key); err != nil && !storage.IsNotFound(err) {
			artifactErr = err
			if artifact.Bytes > 0 {
				retained += artifact.Bytes
			}
			g.metrics.failure("delete_artifact")
			g.p.Log.Warn("grace: app artifact delete failed", "app", app.ID,
				"key", artifact.Key, "bytes", artifact.Bytes, "err", err)
		}
	}
	if artifactErr != nil {
		return retained, artifactErr
	}
	g.deleteImageBase(ctx, app.ID)
	if err := g.p.Store.DeleteAppPermanently(ctx, app.ID); err != nil {
		g.purgeRefused(ctx, app, err, now)
		return 0, err
	}
	delete(g.blocked, app.ID)
	g.metrics.deleted.Inc()
	// Reuse the existing lifecycle channel so schedd's cleanup path
	// gets a durable redelivery signal. The app row has already been
	// removed, so consumers treat a missing row as an idempotent no-op.
	payload, _ := json.Marshal(map[string]string{"app_id": app.ID, "slug": app.Slug})
	if err := g.p.Notif(ctx, db.NotifyAppDelete, string(payload)); err != nil {
		g.p.Log.Warn("grace: app delete notify failed", "app", app.ID, "err", err)
	}
	accountID := app.AccountID
	g.p.Audit.Emit(ctx, "app.deleted", &accountID, map[string]any{
		"app_id": app.ID, "slug": app.Slug, "actor": actor,
	})
	g.p.Log.Info("grace: app hard-deleted", "app", app.ID, "slug", app.Slug, "actor", actor)
	return 0, nil
}
