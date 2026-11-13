package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Instance identity file (ADR-20261107160000). A process's environment is
// fixed at exec, so a workload restored from a snapshot keeps the
// FAAS_INSTANCE_ID, FAAS_NODE_ID and FAAS_REGION of the instance that captured
// it. guest-init writes the serving instance's identity to a tmpfs file at
// boot and rewrites it on every resume, before the userspace reseed and the
// app's after_restore callback, so code that reads the file always sees the
// instance it runs on.
const (
	InstanceIdentityEnv  = "FAAS_INSTANCE_IDENTITY_FILE"
	InstanceIdentityPath = "/run/guest-init/identity.json"

	instanceIdentitySourceBoot   = "boot"
	instanceIdentitySourceResume = "resume"
)

// instanceIdentityPath is where the publish helpers write; tests point it at
// a temporary directory.
var instanceIdentityPath = InstanceIdentityPath

// instanceIdentity is the file's JSON shape. Fields are additive only.
type instanceIdentity struct {
	InstanceID string `json:"instance_id"`
	NodeID     string `json:"node_id"`
	Region     string `json:"region"`
	// Source is "boot" for the values the process started with and "resume"
	// once a resume hook has replaced them.
	Source    string `json:"source"`
	UpdatedAt string `json:"updated_at"`
}

// instanceIdentityEnabled is set once the boot write succeeded. Env stamping
// reads it, so a workload is never pointed at a file that does not exist.
var instanceIdentityEnabled atomic.Bool

// instanceIdentityFromAPIEnv takes the platform-owned keys schedd appends to
// env.json at wake.
func instanceIdentityFromAPIEnv(env map[string]string) instanceIdentity {
	return instanceIdentity{
		InstanceID: env[api.PlatformInstanceIDEnv],
		NodeID:     env[api.PlatformNodeIDEnv],
		Region:     env[api.PlatformRegionEnv],
		Source:     instanceIdentitySourceBoot,
	}
}

// writeInstanceIdentityFile atomically replaces path. Readers see either the
// old or the new complete document. The file is world-readable: it holds
// placement metadata, never credentials.
func writeInstanceIdentityFile(path string, id instanceIdentity, now time.Time) error {
	if id.InstanceID == "" {
		return fmt.Errorf("instance identity: empty instance id")
	}
	id.UpdatedAt = now.UTC().Format(time.RFC3339Nano)
	data, err := json.Marshal(id)
	if err != nil {
		return fmt.Errorf("instance identity: marshal: %w", err)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("instance identity: create dir: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".identity-*.json")
	if err != nil {
		return fmt.Errorf("instance identity: create temp: %w", err)
	}
	tmpName := tmp.Name()
	_, err = tmp.Write(append(data, '\n'))
	if chmodErr := tmp.Chmod(0o444); err == nil {
		err = chmodErr
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(tmpName, path)
	}
	if err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("instance identity: publish: %w", err)
	}
	return nil
}

// publishBootInstanceIdentity writes the boot identity and enables stamping.
// Guests without a platform instance ID (builders, legacy fixtures) get no
// file and no variable.
func publishBootInstanceIdentity(apiEnv map[string]string, now time.Time) error {
	id := instanceIdentityFromAPIEnv(apiEnv)
	if id.InstanceID == "" {
		return nil
	}
	if err := writeInstanceIdentityFile(instanceIdentityPath, id, now); err != nil {
		return err
	}
	instanceIdentityEnabled.Store(true)
	return nil
}

// publishResumedInstanceIdentity replaces the file with the identity a resume
// hook carried. An older vmmd sends none and the previous file stays, which
// is still the identity of an instance this guest has served as.
func publishResumedInstanceIdentity(instanceID, nodeID, region string, now time.Time) error {
	if instanceID == "" || !instanceIdentityEnabled.Load() {
		return nil
	}
	return writeInstanceIdentityFile(instanceIdentityPath, instanceIdentity{
		InstanceID: instanceID, NodeID: nodeID, Region: region, Source: instanceIdentitySourceResume,
	}, now)
}

// StampInstanceIdentityEnv points the workload at the identity file. The
// platform owns the value; a workload's own setting is replaced.
func StampInstanceIdentityEnv(env []string) []string {
	if !instanceIdentityEnabled.Load() {
		return env
	}
	return setEnvValue(env, InstanceIdentityEnv, instanceIdentityPath)
}
