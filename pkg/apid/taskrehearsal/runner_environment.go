package taskrehearsal

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/tasktoolbinding"
)

const (
	migrationDatabaseKey = "MCP_TASK_MIGRATION_DATABASE_URL"
	namespaceKey         = "MCP_TASK_NAMESPACE"
)

type runnerEnvironment struct {
	roles        map[string]map[string]string
	databaseKeys map[string]string
}

// ValidateRunnerPolicy refuses aliases between database, namespace and key
// material before the private claim consumes the approval.
func ValidateRunnerPolicy(policy tasktoolbinding.Policy) error {
	if !policy.Valid() {
		return state.ErrInvalidArgument
	}
	web := []string{policy.WebDatabaseEnv, policy.WebOwnerEnv}
	worker := []string{policy.WorkerDatabaseEnv, policy.WorkerOwnerEnv}
	for _, key := range policy.PreviousKeys {
		web = append(web, key.WebEnv)
		worker = append(worker, key.WorkerEnv)
	}
	if policy.WebPayloadEnv != "" {
		web = append(web, policy.WebPayloadEnv)
		worker = append(worker, policy.WorkerPayloadEnv)
	}
	if !validRunnerKeys(policy.WebNamespaceEnv, web) || !validRunnerKeys(policy.WorkerNamespaceEnv, worker) {
		return state.ErrInvalidArgument
	}
	return nil
}

func validRunnerKeys(namespace string, keys []string) bool {
	seen := map[string]bool{namespaceKey: true, migrationDatabaseKey: true}
	if namespace != namespaceKey {
		keys = append(keys, namespace)
	}
	for _, key := range keys {
		upper := strings.ToUpper(key)
		if seen[upper] || api.PlatformProvidedEnvKey(upper) || upper == "PATH" || upper == "NODE_OPTIONS" ||
			upper == "NODE_PATH" || strings.HasPrefix(upper, "LD_") || strings.HasPrefix(upper, "PG") {
			return false
		}
		seen[upper] = true
	}
	return true
}

func newRunnerEnvironment(namespace string, policy tasktoolbinding.Policy) (*runnerEnvironment, error) {
	if err := ValidateRunnerPolicy(policy); err != nil {
		return nil, err
	}
	randomKey := func() string {
		var raw [32]byte
		_, _ = rand.Read(raw[:]) // Go 1.26 fills the buffer or terminates the process.
		return hex.EncodeToString(raw[:])
	}
	owner := randomKey()
	web := map[string]string{namespaceKey: namespace, policy.WebNamespaceEnv: namespace, policy.WebOwnerEnv: owner}
	worker := map[string]string{namespaceKey: namespace, policy.WorkerNamespaceEnv: namespace, policy.WorkerOwnerEnv: owner}
	for _, key := range policy.PreviousKeys {
		value := randomKey()
		web[key.WebEnv], worker[key.WorkerEnv] = value, value
	}
	if policy.WebPayloadEnv != "" {
		active := "rehearsal"
		if policy.PayloadVersion < 2 {
			active = "legacy"
		}
		value, err := json.Marshal(struct {
			Active string            `json:"activeKeyId"`
			Keys   map[string]string `json:"keys"`
		}{Active: active, Keys: map[string]string{"rehearsal": randomKey()}})
		if err != nil {
			return nil, err
		}
		web[policy.WebPayloadEnv], worker[policy.WorkerPayloadEnv] = string(value), string(value)
	}
	migration := make(map[string]string, len(worker)+1)
	for key, value := range worker {
		migration[key] = value
	}
	return &runnerEnvironment{
		roles: map[string]map[string]string{
			Migration: migration, Web: web, Worker: worker, Observer: {namespaceKey: namespace},
		},
		databaseKeys: map[string]string{
			Migration: migrationDatabaseKey, Web: policy.WebDatabaseEnv, Worker: policy.WorkerDatabaseEnv, Observer: databaseKey,
		},
	}, nil
}
