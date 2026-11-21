package state

// Bucket notification destinations resolve in the bucket's environment
// (ADR-20261104120000 amendment 2026-11-16). The environment is the one the
// bucket's events carry: a branch's copy bucket or a clone's target bucket is
// its stage's, every other bucket production's.

// notificationEnvironment is a bucket's environment for its notification
// rules. The zero value is production. Gone marks a stage bucket whose
// environment no longer exists: its rules resolve nothing.
type notificationEnvironment struct {
	Scope string
	ID    string
	Gone  bool
}

// objectBucketNotificationEnvironmentLocked mirrors
// pgObjectBucketNotificationEnvironment.
func (m *MemStore) objectBucketNotificationEnvironmentLocked(bucketID string) notificationEnvironment {
	id := m.objectBucketEventEnvironmentLocked(bucketID)
	if id == "" {
		return notificationEnvironment{}
	}
	for _, env := range m.projectEnvironments {
		if env.ID == id {
			return notificationEnvironment{Scope: env.Slug, ID: env.ID}
		}
	}
	return notificationEnvironment{Gone: true}
}

// queueInNotificationEnvironment reports whether a binding belongs to the
// environment: production's are unscoped, a stage's carry its ID.
func queueInNotificationEnvironment(b QueueBinding, env notificationEnvironment) bool {
	if env.ID == "" {
		return b.DeploymentScope == "" && b.EnvironmentID == ""
	}
	return b.DeploymentScope == env.Scope && (b.EnvironmentID == env.ID || notificationSameIdentity(b.EnvironmentID, env.ID))
}

// functionInNotificationEnvironmentLocked reports whether an app can run in
// the environment: every app runs in production, and a stage is one of its
// project's environments.
func (m *MemStore) functionInNotificationEnvironmentLocked(app App, env notificationEnvironment) bool {
	if env.ID == "" {
		return true
	}
	stage, ok := m.projectEnvironments[env.ID]
	if !ok {
		for _, e := range m.projectEnvironments {
			if e.ID == env.ID {
				stage, ok = e, true
				break
			}
		}
	}
	return ok && app.ProjectID != "" && stage.ProjectID == app.ProjectID && stage.Slug == env.Scope
}
