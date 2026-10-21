//go:build metal

package e2e_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

func TestProjectReleaseSecretsPromotionMetal(t *testing.T) {
	qualifyProjectProgressiveFailureMode(t, false, false, true, "secrets_promotion", "")
}
func TestProjectReleaseSecretsRollbackMetal(t *testing.T) {
	qualifyProjectProgressiveFailureMode(t, false, false, true, "secrets_rollback", "")
}

type nativeReleaseSecretRotation struct {
	current, previous                    *age.X25519Identity
	dir, public                          string
	rotated, baselineCold, candidateCold bool
}

func newNativeReleaseSecretRotation(t *testing.T) (*nativeReleaseSecretRotation, []string) {
	t.Helper()
	current, err := age.GenerateX25519Identity()
	mustProjectWorkerNative(t, err)
	previous, err := age.GenerateX25519Identity()
	mustProjectWorkerNative(t, err)
	dir := t.TempDir()
	private := filepath.Join(dir, "host.age")
	public := filepath.Join(dir, "host.age.pub")
	legacy := filepath.Join(dir, "legacy.pub")
	for path, id := range map[string]*age.X25519Identity{private: current, filepath.Join(dir, "fleet.age"): current, filepath.Join(dir, "host.age.previous"): previous} {
		mustProjectWorkerNative(t, secretbox.WriteHostKeyAtPath(path, id))
	}
	mustProjectWorkerNative(t, secretbox.WriteRecipientFile(public, current))
	mustProjectWorkerNative(t, secretbox.WriteRecipientFile(legacy, previous))
	// vmmd loads current plus overlap; APID initially seals the release to the
	// previous recipient so its background replayer must migrate every copy.
	return &nativeReleaseSecretRotation{current: current, previous: previous, dir: dir, public: public}, []string{"FAAS_HOST_KEY_PATH=" + private, "FAAS_HOST_AGE_IDENTITY_PATH=" + private, "FAAS_HOST_AGE_RECIPIENT_PATH=" + public, "FAAS_FLEET_AGE_IDENTITY_PATH=" + filepath.Join(dir, "fleet.age"), "FAAS_FLEET_AGE_RECIPIENT_PATH=" + legacy}
}
func (r *nativeReleaseSecretRotation) rotate(t *testing.T, f *projectWorkerNativeFixture, accepted api.ApplyResponse) {
	t.Helper()
	oldKid := r.previous.Recipient().String()
	if err := f.store.RetireSecretEncryptionKey(t.Context(), oldKid); err == nil {
		t.Fatal("native retirement accepted retained old-key secrets")
	}
	mustProjectWorkerNative(t, f.h.SetAPIDEnv("FAAS_FLEET_AGE_RECIPIENT_PATH", r.public))
	mustProjectWorkerNative(t, f.h.SetAPIDEnv("FAAS_REKEY_ENABLED", "true"))
	mustProjectWorkerNative(t, f.h.RestartAPID())
	waitProjectWorkerNative(t, 2*time.Minute, "release secret copies rekeyed by APID", func() bool {
		usage, err := f.store.SecretEncryptionKeyUsage(t.Context(), oldKid)
		mustProjectWorkerNative(t, err)
		return usage.Empty()
	})
	for _, deployment := range []string{f.baseline.Members[0].DeploymentID, accepted.Builds[0].DeploymentID} {
		snapshot, err := f.store.RuntimeAppValuesForDeployment(t.Context(), f.accountID, f.apps["frontend"].ID, deployment)
		mustProjectWorkerNative(t, err)
		if !snapshot.ReleaseSecretsPinned || len(snapshot.Secrets) != 1 {
			t.Fatal("native release secret snapshot missing")
		}
		if _, err := secretbox.Open(r.current, snapshot.Secrets[0].Ciphertext); err != nil {
			t.Fatal("retained envelope cannot open with the current identity")
		}
		if _, err := secretbox.Open(r.previous, snapshot.Secrets[0].Ciphertext); err == nil {
			t.Fatal("retained envelope still opens with the previous identity")
		}
	}
	mustProjectWorkerNative(t, f.store.RetireSecretEncryptionKey(t.Context(), oldKid))
	mustProjectWorkerNative(t, os.Remove(filepath.Join(r.dir, "host.age.previous")))
	mustProjectWorkerNative(t, f.h.KillSchedd())
	mustProjectWorkerNative(t, f.h.RestartVMMD())
	mustProjectWorkerNative(t, f.h.RestartAPID())
	mustProjectWorkerNative(t, f.h.RestartSchedd())
	r.rotated = true
	r.restartFresh(t, f, f.baseline.Members[0].DeploymentID)
	r.baselineCold = true
}
func (r *nativeReleaseSecretRotation) restartFresh(t *testing.T, f *projectWorkerNativeFixture, deployment string) {
	t.Helper()
	started := time.Now().UTC()
	app := f.apps["frontend"]
	restart, err := f.owner.RestartAppFresh(t.Context(), app.Slug)
	mustProjectWorkerNative(t, err)
	waitProjectWorkerNative(t, 2*time.Minute, "release cold boot after encryption key retirement", func() bool {
		status, err := f.owner.GetRuntimeConfigRestartStatus(t.Context(), app.Slug, restart.WakeID)
		mustProjectWorkerNative(t, err)
		if status.Status == "failed" {
			t.Fatalf("fresh restart failed: %s", status.FailureReason)
		}
		return status.Status == "succeeded"
	})
	var cold bool
	mustProjectWorkerNative(t, f.h.Pool.QueryRow(t.Context(), `SELECT EXISTS(SELECT 1 FROM events e JOIN instances i ON i.id::text=e.data->>'instance_id' WHERE i.deployment_id=$1 AND e.actor='schedd' AND e.kind='wake.boot_started' AND e.data->>'method'='cold_boot' AND e.at>=$2)`, deployment, started).Scan(&cold))
	if !cold {
		t.Fatal("native release did not cold boot after key retirement")
	}
}
