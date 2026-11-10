package main

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf16"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const taskTable = "gregale_mcp_tasks"
const taskWorkerHandlersTable = "gregale_mcp_task_worker_handlers"
const maxTaskInputStateBytes = 256 * 1024

var taskIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-8][0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$`)
var taskKeyIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)
var taskHandlerVersionPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// MCPTaskExecutor runs one durable version of an MCP tool. Executors must be
// safe to replay after a worker restart and should use TaskID as an idempotency
// key for external side effects.
type MCPTaskExecutor func(context.Context, map[string]any, *MCPTaskExecutionContext) (map[string]any, error)

// MCPTaskHandlerDefinition selects the version written for new tasks and the
// older versions this deployment can still execute. Validate checks new task
// arguments before they are stored.
type MCPTaskHandlerDefinition struct {
	Version          string
	PreviousVersions map[string]MCPTaskExecutor
	Validate         func(map[string]any) error
	Execute          MCPTaskExecutor
}

// MCPTaskExecutionContext exposes durable input requests to a task executor.
// If the client has not answered yet, RequestInputs returns
// ErrMCPTaskInputRequired and the task is safely resumed from its beginning.
type MCPTaskExecutionContext struct {
	TaskID        string
	requestInputs func(map[string]any) (map[string]any, error)
}

func (execution *MCPTaskExecutionContext) RequestInputs(requests map[string]any) (map[string]any, error) {
	if execution == nil || execution.requestInputs == nil {
		return nil, errors.New("MCP task input requests are unavailable")
	}
	return execution.requestInputs(requests)
}

func (execution *MCPTaskExecutionContext) RequestInput(key string, request map[string]any) (any, error) {
	responses, err := execution.RequestInputs(map[string]any{key: request})
	if err != nil {
		return nil, err
	}
	response, ok := responses[key]
	if !ok {
		return nil, errors.New("MCP task input response is unavailable")
	}
	return response, nil
}

var ErrMCPTaskInputRequired = errors.New("MCP task is waiting for client input")

type mcpTaskHandlerKey struct {
	toolName string
	version  string
}

type mcpRegisteredTaskHandler struct {
	definition MCPTaskHandlerDefinition
	executors  map[string]MCPTaskExecutor
}

var mcpTaskHandlers = struct {
	sync.RWMutex
	entries map[string]mcpRegisteredTaskHandler
}{entries: make(map[string]mcpRegisteredTaskHandler)}

// RegisterMCPTaskHandler registers the durable executor for a catalog tool.
// Register the same definition in every web and worker process sharing a task
// queue. Each retained previous version is eligible for worker claims.
func RegisterMCPTaskHandler(name string, definition MCPTaskHandlerDefinition) error {
	if !validCatalogName(name) || !taskHandlerVersionPattern.MatchString(definition.Version) || definition.Validate == nil || definition.Execute == nil {
		return errors.New("MCP task handlers require a valid tool name, active version, validator, and executor")
	}
	executors := make(map[string]MCPTaskExecutor, len(definition.PreviousVersions)+1)
	for version, execute := range definition.PreviousVersions {
		if !taskHandlerVersionPattern.MatchString(version) || version == definition.Version || execute == nil {
			return errors.New("MCP task previous handler versions are invalid")
		}
		executors[version] = execute
	}
	executors[definition.Version] = definition.Execute
	definition.PreviousVersions = make(map[string]MCPTaskExecutor, len(executors)-1)
	for version, execute := range executors {
		if version != definition.Version {
			definition.PreviousVersions[version] = execute
		}
	}
	mcpTaskHandlers.Lock()
	defer mcpTaskHandlers.Unlock()
	if _, exists := mcpTaskHandlers.entries[name]; exists {
		return fmt.Errorf("MCP task handler %q is already registered", name)
	}
	mcpTaskHandlers.entries[name] = mcpRegisteredTaskHandler{definition: definition, executors: executors}
	return nil
}

func snapshotMCPTaskHandlers() (map[string]mcpRegisteredTaskHandler, map[mcpTaskHandlerKey]MCPTaskExecutor) {
	mcpTaskHandlers.RLock()
	defer mcpTaskHandlers.RUnlock()
	active := make(map[string]mcpRegisteredTaskHandler, len(mcpTaskHandlers.entries))
	executors := make(map[mcpTaskHandlerKey]MCPTaskExecutor)
	for name, entry := range mcpTaskHandlers.entries {
		active[name] = entry
		for version, execute := range entry.executors {
			executors[mcpTaskHandlerKey{toolName: name, version: version}] = execute
		}
	}
	return active, executors
}

func init() {
	if err := RegisterMCPTaskHandler("build_report", MCPTaskHandlerDefinition{
		Version: "2", PreviousVersions: map[string]MCPTaskExecutor{"1": executeLegacyBuildReportTask},
		Validate: validateBuildReportTask, Execute: executeBuildReportTask,
	}); err != nil {
		panic(err)
	}
}

type mcpTaskConfig struct {
	EncryptionKeysEnv        string                    `json:"encryption_keys_env"`
	Enabled                  *bool                     `json:"enabled"`
	DatabaseURLEnv           string                    `json:"database_url_env"`
	OwnerKeyEnv              string                    `json:"owner_key_env"`
	OwnerKeyID               string                    `json:"owner_key_id"`
	PreviousOwnerKeys        []mcpTaskPreviousOwnerKey `json:"previous_owner_keys"`
	PayloadEncryptionVersion int                       `json:"payload_encryption_version"`
	NamespaceEnv             string                    `json:"namespace_env"`
	TTLSeconds               *int                      `json:"ttl_seconds"`
	PollIntervalMS           *int                      `json:"poll_interval_ms"`
	WorkerConcurrency        *int                      `json:"worker_concurrency"`
	MaxAttempts              int                       `json:"max_attempts"`
	RetryBaseDelayMS         int                       `json:"retry_base_delay_ms"`
	RetryMaxDelayMS          int                       `json:"retry_max_delay_ms"`
	MaxRunning               int                       `json:"max_running"`
	MaxRunningPerOwner       int                       `json:"max_running_per_owner"`
	MaxOutstanding           int                       `json:"max_outstanding"`
	MaxOutstandingPerOwner   int                       `json:"max_outstanding_per_owner"`
}

type mcpTaskPreviousOwnerKey struct {
	ID  string `json:"id"`
	Env string `json:"env"`
}

type mcpTaskKey struct {
	mainAAD      bool
	id           string
	ownerHashKey []byte
	payloadKey   []byte
}

type durableMCPTask struct {
	ID             string
	ToolName       string
	HandlerVersion string
	Status         string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	ExpiresAt      time.Time
	AttemptCount   int
	LeaseToken     string
	leaseDeadline  time.Time
	CancelAt       *time.Time
	Arguments      map[string]any
	InputMethods   []string
	InputRequests  map[string]any
	Result         any
	Error          any
	OutputSchema   json.RawMessage
}

type taskInputState struct {
	Requests  map[string]any `json:"requests"`
	Responses map[string]any `json:"responses"`
}

var errMCPTaskNotFound = errors.New("MCP task not found")
var errInvalidMCPTaskInput = errors.New("invalid MCP task input")
var errTaskInputLeaseLost = errors.New("MCP task lease was lost while requesting input")
var errTaskInputCancelled = errors.New("MCP task was cancelled while requesting input")

type mcpTaskRuntime struct {
	pool                     *pgxpool.Pool
	role                     string
	namespace                string
	notificationChannel      string
	ttl                      time.Duration
	pollInterval             time.Duration
	concurrency              int
	maxOutstanding           int
	maxPerOwner              int
	execution                mcpTaskExecutionPolicy
	payloadEncryptionVersion int
	rewrapMu                 sync.Mutex
	rewrapAfter              string
	rewrapThrough            string
	taskKeys                 []mcpTaskKey
	taskHandlers             map[string]mcpRegisteredTaskHandler
	taskExecutors            map[mcpTaskHandlerKey]MCPTaskExecutor
	drainTimeout             time.Duration
	drainMu                  sync.Mutex
	drainStarted             time.Time
	drainTimer               *time.Timer
	drainExpired             bool
	drainPending             []*durableMCPTask
	activeTasks              map[string]*durableMCPTask
	executionCtx             context.Context
	executionCancel          context.CancelFunc
	closeOnce                sync.Once
	workerID                 string
	workerRegistryMu         sync.Mutex
	deploymentID             string
	rewrapCancel             context.CancelFunc
	rewrapDone               chan struct{}
	cancel                   context.CancelFunc
	done                     chan struct{}
	metrics                  *mcpTaskMetricsPublisher
	subscriptionMu           sync.Mutex
	subscriptions            int
	watchedTasks             int
	taskNotifyMu             sync.RWMutex
	taskNotifyWatchers       map[string]map[chan string]struct{}
	taskNotifyCancel         context.CancelFunc
	taskNotifyDone           chan struct{}
}

func startMCPTaskRuntime(config mcpConfig, role string) (*mcpTaskRuntime, error) {
	drainSeconds := 10
	if raw := os.Getenv("MCP_DRAIN_TIMEOUT_SECONDS"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 300 {
			return nil, errors.New("MCP_DRAIN_TIMEOUT_SECONDS must be an integer between 1 and 300")
		}
		drainSeconds = value
	}
	if role != "combined" && role != "web" && role != "worker" && role != "observer" {
		return nil, errors.New("MCP task role must be combined, web, worker, or observer")
	}
	if len(config.Tasks) == 0 || string(config.Tasks) == "null" {
		if role == "worker" || role == "observer" {
			return nil, errors.New("MCP Tasks worker or observer requires tasks.enabled=true")
		}
		return nil, nil
	}
	var settings mcpTaskConfig
	if err := json.Unmarshal(config.Tasks, &settings); err != nil {
		return nil, errors.New("MCP task settings are invalid")
	}
	if settings.Enabled == nil {
		return nil, errors.New("tasks.enabled must be explicitly true or false when task settings are present")
	}
	if !*settings.Enabled {
		if role == "worker" || role == "observer" {
			return nil, errors.New("MCP Tasks worker or observer requires tasks.enabled=true")
		}
		return nil, nil
	}
	execution, err := resolveTaskExecutionPolicy(settings)
	if err != nil {
		return nil, err
	}
	if settings.DatabaseURLEnv == "" {
		settings.DatabaseURLEnv = "DATABASE_URL"
	}
	if settings.OwnerKeyEnv == "" {
		settings.OwnerKeyEnv = "MCP_TASK_OWNER_KEY"
	}
	if settings.OwnerKeyID == "" {
		settings.OwnerKeyID = "default"
	}
	if settings.PayloadEncryptionVersion == 0 {
		settings.PayloadEncryptionVersion = 1
	}
	if settings.NamespaceEnv == "" {
		settings.NamespaceEnv = "FAAS_APP_ID"
	}
	if !taskKeyIDPattern.MatchString(settings.OwnerKeyID) || len(settings.PreviousOwnerKeys) > 16 || (settings.PayloadEncryptionVersion != 1 && settings.PayloadEncryptionVersion != 2) {
		return nil, errors.New("MCP task owner key IDs or previous_owner_keys are invalid")
	}
	seenKeyIDs := map[string]bool{settings.OwnerKeyID: true}
	envNames := []string{settings.DatabaseURLEnv, settings.OwnerKeyEnv, settings.NamespaceEnv}
	for _, previous := range settings.PreviousOwnerKeys {
		if !taskKeyIDPattern.MatchString(previous.ID) || seenKeyIDs[previous.ID] || previous.Env == "" {
			return nil, errors.New("MCP task previous owner key configuration is invalid")
		}
		seenKeyIDs[previous.ID] = true
		envNames = append(envNames, previous.Env)
	}
	for _, name := range envNames {
		if !regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`).MatchString(name) {
			return nil, errors.New("MCP task environment variable name is invalid")
		}
	}
	databaseURL := os.Getenv(settings.DatabaseURLEnv)
	ownerKey := os.Getenv(settings.OwnerKeyEnv)
	namespaceOverride := strings.TrimSpace(os.Getenv("MCP_TASK_NAMESPACE"))
	if role != "combined" && namespaceOverride == "" {
		return nil, fmt.Errorf("MCP Tasks role %s requires MCP_TASK_NAMESPACE to match every app sharing the queue", role)
	}
	namespace := namespaceOverride
	if namespace == "" {
		namespace = strings.TrimSpace(os.Getenv(settings.NamespaceEnv))
	}
	if databaseURL == "" || (role != "observer" && (ownerKey == "" || len([]byte(ownerKey)) < 32)) || namespace == "" || len(namespace) > 255 {
		return nil, errors.New("MCP Tasks require a PostgreSQL URL, a 32-byte owner key, and a stable namespace")
	}
	metricsSettings, err := resolveMCPTaskMetricsSettings(role)
	if err != nil {
		return nil, err
	}
	if role == "observer" && metricsSettings == nil {
		return nil, errors.New("MCP task observer requires scaling metric credentials")
	}
	ttlSeconds, pollIntervalMS, workerConcurrency := 86400, 2000, 1
	if settings.TTLSeconds != nil && *settings.TTLSeconds != 0 {
		ttlSeconds = *settings.TTLSeconds
	}
	if settings.PollIntervalMS != nil && *settings.PollIntervalMS != 0 {
		pollIntervalMS = *settings.PollIntervalMS
	}
	if settings.WorkerConcurrency != nil && *settings.WorkerConcurrency != 0 {
		workerConcurrency = *settings.WorkerConcurrency
	}
	if settings.MaxOutstanding == 0 {
		settings.MaxOutstanding = 1000
	}
	if settings.MaxOutstandingPerOwner == 0 {
		settings.MaxOutstandingPerOwner = 100
	}
	if ttlSeconds < 60 || ttlSeconds > 30*24*60*60 || pollIntervalMS < 500 || pollIntervalMS > 30000 || workerConcurrency < 1 || workerConcurrency > 16 || settings.MaxOutstanding < 1 || settings.MaxOutstandingPerOwner < 1 || settings.MaxOutstandingPerOwner > settings.MaxOutstanding {
		return nil, errors.New("MCP task limits are outside their supported ranges")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	poolConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, errors.New("could not initialize the PostgreSQL MCP task store")
	}
	poolWorkers := 0
	if role == "combined" || role == "worker" {
		poolWorkers = workerConcurrency
	}
	configureTaskDatabasePool(poolConfig, poolWorkers)
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, errors.New("could not initialize the PostgreSQL MCP task store")
	}
	activeTaskHandlers, taskExecutors := snapshotMCPTaskHandlers()
	runtime := &mcpTaskRuntime{
		pool: pool, role: role, namespace: namespace, notificationChannel: taskNotificationChannel(namespace),
		deploymentID:       strings.TrimSpace(os.Getenv("FAAS_DEPLOYMENT_ID")),
		taskNotifyWatchers: make(map[string]map[chan string]struct{}), ttl: time.Duration(ttlSeconds) * time.Second,
		pollInterval: time.Duration(pollIntervalMS) * time.Millisecond,
		concurrency:  workerConcurrency, maxOutstanding: settings.MaxOutstanding,
		execution:   execution,
		maxPerOwner: settings.MaxOutstandingPerOwner, payloadEncryptionVersion: settings.PayloadEncryptionVersion,
		taskHandlers: activeTaskHandlers, taskExecutors: taskExecutors,
	}
	if role == "combined" || role == "worker" {
		runtime.workerID, err = newTaskUUID()
		if err != nil {
			pool.Close()
			return nil, errors.New("could not create the MCP task worker identity")
		}
	}
	if role != "observer" {
		keySpecs := append([]mcpTaskPreviousOwnerKey{{ID: settings.OwnerKeyID, Env: settings.OwnerKeyEnv}}, settings.PreviousOwnerKeys...)
		for _, spec := range keySpecs {
			secret := os.Getenv(spec.Env)
			if len([]byte(secret)) < 32 {
				pool.Close()
				return nil, errors.New("MCP task owner and previous keys must contain at least 32 bytes")
			}
			masterKey := []byte(secret)
			runtime.taskKeys = append(runtime.taskKeys, mcpTaskKey{
				id: spec.ID, ownerHashKey: deriveTaskKey(masterKey, "gregale-mcp-task-owner:v1"),
				payloadKey: deriveTaskKey(masterKey, "gregale-mcp-task-payload:v1"),
			})
		}
		if settings.EncryptionKeysEnv != "" {
			if !regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`).MatchString(settings.EncryptionKeysEnv) {
				pool.Close()
				return nil, errors.New("invalid task encryption environment name")
			}
			var keys struct {
				ActiveKeyID string            `json:"activeKeyId"`
				Keys        map[string]string `json:"keys"`
			}
			decoder := json.NewDecoder(strings.NewReader(os.Getenv(settings.EncryptionKeysEnv)))
			decoder.DisallowUnknownFields()
			if decoder.Decode(&keys) != nil || decoder.Decode(new(any)) != io.EOF || len(keys.Keys) < 1 || len(keys.Keys) > 16 {
				pool.Close()
				return nil, errors.New("invalid MCP task encryption keys")
			}
			ownerKey := runtime.taskKeys[0].ownerHashKey
			mainKeys := []mcpTaskKey{{id: "legacy", ownerHashKey: ownerKey, payloadKey: runtime.taskKeys[0].payloadKey}}
			for id, secret := range keys.Keys {
				if id == "legacy" || !regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`).MatchString(id) || len([]byte(secret)) < 32 {
					pool.Close()
					return nil, errors.New("invalid MCP task encryption keys")
				}
				mainKeys = append(mainKeys, mcpTaskKey{id: id, ownerHashKey: ownerKey, payloadKey: deriveTaskKey([]byte(secret), "gregale-mcp-task-payload:v2:"+id), mainAAD: true})
			}
			active := -1
			for i, key := range mainKeys {
				if key.id == keys.ActiveKeyID {
					active = i
					break
				}
			}
			if active < 0 {
				pool.Close()
				return nil, errors.New("unknown active MCP task encryption key")
			}
			mainKeys[0], mainKeys[active] = mainKeys[active], mainKeys[0]
			runtime.taskKeys = append(mainKeys, runtime.taskKeys...)
			runtime.payloadEncryptionVersion = 2
			if keys.ActiveKeyID == "legacy" {
				runtime.payloadEncryptionVersion = 1
			}
		}
		if err := checkTaskSchema(ctx, runtime.pool); err != nil {
			pool.Close()
			return nil, errors.New("MCP Task schema is unavailable or incompatible; run tasks-migrate apply with the migration account")
		}
		if err := runtime.checkTaskPayloadKeys(ctx); err != nil {
			pool.Close()
			return nil, errors.New("MCP Task payload preflight failed; verify retained keys and database connectivity before starting this runtime")
		}
	} else if err := checkTaskSchema(ctx, pool); err != nil {
		pool.Close()
		return nil, errors.New("MCP Task observer schema is unavailable or incompatible; run tasks-migrate apply")
	} else if _, err := runtime.queueMetrics(ctx); err != nil {
		pool.Close()
		return nil, errors.New("MCP task observer health check failed; verify its read-only database grants")
	}
	if role != "observer" && runtime.payloadEncryptionVersion == 2 {
		rewrapCtx, rewrapCancel := context.WithCancel(context.Background())
		runtime.rewrapCancel = rewrapCancel
		runtime.rewrapDone = make(chan struct{})
		go runtime.rewrapLoop(rewrapCtx)
	}
	runtime.drainTimeout = time.Duration(drainSeconds) * time.Second
	runtime.activeTasks = make(map[string]*durableMCPTask)
	runtime.executionCtx, runtime.executionCancel = context.WithCancel(context.Background())
	if role == "combined" || role == "worker" {
		workerCtx, workerCancel := context.WithCancel(context.Background())
		runtime.cancel = workerCancel
		runtime.done = make(chan struct{})
		go runtime.workerLoop(workerCtx)
	}
	if metricsSettings != nil {
		runtime.metrics = startMCPTaskMetricsPublisher(runtime, *metricsSettings, role == "observer")
	}
	if role == "combined" || role == "web" {
		runtime.startTaskNotificationListener()
	}
	return runtime, nil
}

func (runtime *mcpTaskRuntime) beginDrain() {
	if runtime == nil {
		return
	}
	runtime.drainMu.Lock()
	if !runtime.drainStarted.IsZero() {
		runtime.drainMu.Unlock()
		return
	}
	runtime.drainStarted = time.Now()
	runtime.drainTimer = time.AfterFunc(runtime.drainTimeout, runtime.expireDrain)
	count := len(runtime.activeTasks)
	runtime.drainMu.Unlock()
	if runtime.cancel != nil {
		runtime.cancel()
	}
	if runtime.pool != nil && runtime.workerID != "" && runtime.cancel != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := runtime.markWorkerDraining(ctx); err != nil {
			log.Printf("MCP task worker drain registration failed")
		}
		cancel()
	}
	log.Printf("MCP task drain started: active=%d deadline=%s", count, runtime.drainStarted.Add(runtime.drainTimeout).UTC().Format(time.RFC3339))
}

func (runtime *mcpTaskRuntime) expireDrain() {
	runtime.drainMu.Lock()
	if runtime.drainExpired {
		runtime.drainMu.Unlock()
		return
	}
	runtime.drainExpired = true
	for _, task := range runtime.activeTasks {
		runtime.drainPending = append(runtime.drainPending, task)
	}
	runtime.drainMu.Unlock()
	runtime.executionCancel()
}

func (runtime *mcpTaskRuntime) releaseDrainedTask(parent context.Context, task *durableMCPTask) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
	defer cancel()
	_, err := runtime.pool.Exec(ctx, `UPDATE gregale_mcp_tasks SET status=CASE WHEN cancel_requested_at IS NULL THEN 'queued' ELSE 'cancelled' END,
        resume_pending=cancel_requested_at IS NULL,lease_token=NULL,lease_expires_at=NULL,updated_at=clock_timestamp()
        WHERE namespace=$1 AND task_id=$2::uuid AND lease_token=$3::uuid AND status='running'`, runtime.namespace, task.ID, task.LeaseToken)
	if err != nil {
		log.Printf("MCP task drain lease release failed; lease expiry will recover unfinished work")
	}
}

func (runtime *mcpTaskRuntime) close() {
	if runtime == nil {
		return
	}
	runtime.closeOnce.Do(func() {
		runtime.beginDrain()
		if runtime.metrics != nil {
			runtime.metrics.close()
		}
		if runtime.taskNotifyCancel != nil {
			runtime.taskNotifyCancel()
			<-runtime.taskNotifyDone
		}
		if runtime.cancel != nil {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
			_, _ = runtime.pool.Exec(cleanupCtx, `DELETE FROM gregale_mcp_task_worker_handlers WHERE namespace=$1 AND worker_id=$2::uuid`, runtime.namespace, runtime.workerID)
			cleanupCancel()
			timeout := time.NewTimer(time.Until(runtime.drainStarted.Add(runtime.drainTimeout)))
			select {
			case <-runtime.done:
			case <-timeout.C:
			}
			timeout.Stop()
			runtime.expireDrain()
			runtime.drainMu.Lock()
			pending := append([]*durableMCPTask(nil), runtime.drainPending...)
			runtime.drainMu.Unlock()
			var release sync.WaitGroup
			for _, task := range pending {
				release.Add(1)
				go func(task *durableMCPTask) {
					defer release.Done()
					runtime.releaseDrainedTask(context.Background(), task)
				}(task)
			}
			release.Wait()
			outcome := "completed"
			if len(pending) > 0 {
				outcome = "timed_out"
			}
			log.Printf("MCP task drain finished: outcome=%s unfinished=%d", outcome, len(pending))
		}
		runtime.drainTimer.Stop()
		if runtime.rewrapCancel != nil {
			runtime.rewrapCancel()
			<-runtime.rewrapDone
		}
		if runtime.cancel != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = runtime.removeWorkerRegistration(ctx)
			cancel()
		}
		runtime.pool.Close()
	})
}

func migrateTaskSchema(ctx context.Context, pool *pgxpool.Pool) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SET LOCAL lock_timeout = '5s'`); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, taskTable); err != nil {
		return err
	}
	if err := checkTaskMigrationSchema(ctx, tx); err != nil {
		return err
	}
	var installed bool
	if err := tx.QueryRow(ctx, `SELECT to_regclass('gregale_mcp_task_starter_schema') IS NOT NULL`).Scan(&installed); err != nil {
		return err
	}
	if installed {
		var version int
		if err := tx.QueryRow(ctx, `SELECT version FROM gregale_mcp_task_starter_schema WHERE id=1`).Scan(&version); err != nil || version != 1 {
			return errors.New("unsupported MCP Task starter schema version")
		}
	}
	statements := []string{
		`CREATE TABLE IF NOT EXISTS gregale_mcp_task_starter_schema (id integer PRIMARY KEY CHECK (id=1), version integer NOT NULL CHECK (version>0))`,
		`INSERT INTO gregale_mcp_task_starter_schema (id,version) VALUES (1,1) ON CONFLICT (id) DO NOTHING`,
		`CREATE TABLE IF NOT EXISTS gregale_mcp_tasks (
			namespace text NOT NULL, task_id uuid NOT NULL, owner_hash bytea NOT NULL,
			tool_name text NOT NULL, handler_version text NOT NULL, arguments_encrypted bytea NOT NULL,
			status text NOT NULL CHECK (status IN ('queued','running','input_required','completed','cancelled','failed')),
			result_encrypted bytea, error_encrypted bytea, input_state_encrypted bytea,
			output_schema jsonb CHECK(output_schema IS NULL OR jsonb_typeof(output_schema)='object'),
			input_methods text[] NOT NULL DEFAULT '{}', created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
			updated_at timestamptz NOT NULL DEFAULT clock_timestamp(), expires_at timestamptz NOT NULL,
			attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count >= 0), resume_pending boolean NOT NULL DEFAULT false,
			next_attempt_at timestamptz,
			lease_token uuid, lease_expires_at timestamptz, cancel_requested_at timestamptz,
			PRIMARY KEY(namespace, task_id),
			CHECK ((status = 'running') = (lease_token IS NOT NULL AND lease_expires_at IS NOT NULL)),
			CHECK ((lease_token IS NULL) = (lease_expires_at IS NULL)))`,
		`ALTER TABLE gregale_mcp_tasks ADD COLUMN IF NOT EXISTS input_state_encrypted bytea,
			ADD COLUMN IF NOT EXISTS input_methods text[] NOT NULL DEFAULT '{}',
			ADD COLUMN IF NOT EXISTS resume_pending boolean NOT NULL DEFAULT false,
			ADD COLUMN IF NOT EXISTS next_attempt_at timestamptz`,
		`ALTER TABLE gregale_mcp_tasks ADD COLUMN IF NOT EXISTS output_schema jsonb CHECK(output_schema IS NULL OR jsonb_typeof(output_schema)='object')`,
		`ALTER TABLE gregale_mcp_tasks DROP CONSTRAINT IF EXISTS gregale_mcp_tasks_status_check`,
		`ALTER TABLE gregale_mcp_tasks ADD CONSTRAINT gregale_mcp_tasks_status_check
			CHECK (status IN ('queued','running','input_required','completed','cancelled','failed'))`,
		`ALTER TABLE gregale_mcp_tasks DROP CONSTRAINT IF EXISTS gregale_mcp_tasks_input_state_check`,
		`ALTER TABLE gregale_mcp_tasks ADD CONSTRAINT gregale_mcp_tasks_input_state_check CHECK (status <> 'input_required' OR input_state_encrypted IS NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS gregale_mcp_tasks_queue_idx ON gregale_mcp_tasks(namespace,created_at,task_id) WHERE status IN ('queued','running')`,
		`CREATE INDEX IF NOT EXISTS gregale_mcp_tasks_admission_idx ON gregale_mcp_tasks(namespace,owner_hash,expires_at) WHERE status IN ('queued','running','input_required')`,
		`CREATE TABLE IF NOT EXISTS gregale_mcp_task_owners (
 namespace text NOT NULL, owner_hash bytea NOT NULL, owner_group bytea NOT NULL,
 PRIMARY KEY(namespace,owner_hash), CHECK(octet_length(owner_hash)>0), CHECK(octet_length(owner_group)>0));
 CREATE INDEX IF NOT EXISTS gregale_mcp_task_owners_group_idx ON gregale_mcp_task_owners(namespace,owner_group)`,
		`CREATE TABLE IF NOT EXISTS gregale_mcp_task_worker_handlers (
			namespace text NOT NULL, worker_id uuid NOT NULL, tool_name text NOT NULL,
			handler_version text NOT NULL, deployment_id text NOT NULL DEFAULT '',
			heartbeat_at timestamptz NOT NULL DEFAULT clock_timestamp(),
			PRIMARY KEY(namespace,worker_id,tool_name,handler_version))`,
		`ALTER TABLE gregale_mcp_task_worker_handlers ADD COLUMN IF NOT EXISTS deployment_id text NOT NULL DEFAULT ''`,
		`CREATE INDEX IF NOT EXISTS gregale_mcp_task_worker_handlers_live_idx ON gregale_mcp_task_worker_handlers(namespace,tool_name,handler_version,heartbeat_at DESC)`,
		`CREATE INDEX IF NOT EXISTS gregale_mcp_task_worker_handlers_stale_idx ON gregale_mcp_task_worker_handlers(namespace,heartbeat_at)`,
		`CREATE TABLE IF NOT EXISTS gregale_mcp_task_workers (
 namespace text NOT NULL,worker_id uuid NOT NULL,
 handlers jsonb NOT NULL CHECK(jsonb_typeof(handlers)='array'),draining boolean NOT NULL DEFAULT false,
 heartbeat_at timestamptz NOT NULL,expires_at timestamptz NOT NULL,PRIMARY KEY(namespace,worker_id));
 ALTER TABLE gregale_mcp_task_workers ADD COLUMN IF NOT EXISTS draining boolean NOT NULL DEFAULT false`,
		`CREATE OR REPLACE FUNCTION gregale_mcp_task_notify_change() RETURNS trigger
			LANGUAGE plpgsql AS $$
			BEGIN
				IF TG_OP = 'INSERT' THEN
					PERFORM pg_notify('gregale_mcp_task_' || md5(NEW.namespace), NEW.task_id::text);
				ELSIF current_setting('gregale.mcp_task_rewrap',true) IS DISTINCT FROM 'on' AND (
					NEW.status IS DISTINCT FROM OLD.status OR
					NEW.updated_at IS DISTINCT FROM OLD.updated_at OR
					NEW.result_encrypted IS DISTINCT FROM OLD.result_encrypted OR
					NEW.error_encrypted IS DISTINCT FROM OLD.error_encrypted OR
					NEW.input_state_encrypted IS DISTINCT FROM OLD.input_state_encrypted) THEN
					PERFORM pg_notify('gregale_mcp_task_' || md5(NEW.namespace), NEW.task_id::text);
				END IF;
				RETURN NEW;
			END;
			$$`,
		`DO $body$
			BEGIN
				IF NOT EXISTS (
					SELECT 1 FROM pg_trigger
					 WHERE tgrelid = 'gregale_mcp_tasks'::regclass
					   AND tgname = 'gregale_mcp_tasks_notify'
					   AND NOT tgisinternal
				) THEN
					CREATE TRIGGER gregale_mcp_tasks_notify
						AFTER INSERT OR UPDATE ON gregale_mcp_tasks
						FOR EACH ROW EXECUTE FUNCTION gregale_mcp_task_notify_change();
				END IF;
			END;
			$body$`,
	}
	for _, statement := range statements {
		if _, err := tx.Exec(ctx, statement); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func deriveTaskKey(key []byte, purpose string) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(purpose))
	return mac.Sum(nil)
}

func (runtime *mcpTaskRuntime) ownerHashes(identity *authContext) [][]byte {
	principals := []string{"open"}
	if identity != nil && identity.subject != "" {
		resource, subject, client := taskJSONString(identity.resource), taskJSONString(identity.subject), taskJSONString(identity.clientID)
		principals = []string{
			`[` + resource + `,` + subject + `,` + client + `]`,
			// Earlier Go starters added a second pair of quotes around each
			// JSON string. Keep those retained tasks readable during upgrades.
			`["` + resource + `","` + subject + `","` + client + `"]`,
		}
	}
	result := make([][]byte, 0, len(runtime.taskKeys)*len(principals))
	seen := map[string]bool{}
	for _, key := range runtime.taskKeys {
		for _, principal := range principals {
			mac := hmac.New(sha256.New, key.ownerHashKey)
			_, _ = mac.Write([]byte(principal))
			owner := mac.Sum(nil)
			if !seen[string(owner)] {
				result = append(result, owner)
				seen[string(owner)] = true
			}
		}
	}
	return result
}

func taskOwnerHashPredicate(firstParameter, count int) string {
	values := make([]string, count)
	for index := range values {
		values[index] = fmt.Sprintf("decode($%d,'hex')", firstParameter+index)
	}
	return "owner_hash IN (" + strings.Join(values, ",") + ")"
}

func taskOwnerHashParams(hashes [][]byte) []any {
	values := make([]any, 0, len(hashes))
	for _, hash := range hashes {
		values = append(values, fmt.Sprintf("%x", hash))
	}
	return values
}

func taskJSONString(value string) string {
	var out strings.Builder
	out.WriteByte('"')
	for _, r := range value {
		switch r {
		case '"', '\\':
			out.WriteByte('\\')
			out.WriteRune(r)
		case '\b':
			out.WriteString(`\b`)
		case '\f':
			out.WriteString(`\f`)
		case '\n':
			out.WriteString(`\n`)
		case '\r':
			out.WriteString(`\r`)
		case '\t':
			out.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(&out, `\u%04x`, r)
			} else {
				out.WriteRune(r)
			}
		}
	}
	out.WriteByte('"')
	return out.String()
}

func (runtime *mcpTaskRuntime) encrypt(value any, taskID, field string, maxBytes int) ([]byte, error) {
	plain, err := json.Marshal(value)
	if err != nil || len(plain) > maxBytes {
		return nil, errors.New("MCP task JSON payload is invalid or too large")
	}
	return runtime.encryptPayload(plain, taskID, field)
}

func (runtime *mcpTaskRuntime) encryptPayload(plain []byte, taskID, field string) ([]byte, error) {
	if len(runtime.taskKeys) == 0 {
		return nil, errors.New("MCP task encryption keys are unavailable")
	}
	key := runtime.taskKeys[0]
	block, err := aes.NewCipher(key.payloadKey)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	aad := []byte("gregale-mcp-task:v1:" + runtime.namespace + ":" + taskID + ":" + field)
	if key.mainAAD && runtime.payloadEncryptionVersion == 2 {
		aad = append(aad, []byte(":key:"+key.id)...)
	}
	sealed := aead.Seal(nil, nonce, plain, aad)
	tagAt := len(sealed) - aead.Overhead()
	if runtime.payloadEncryptionVersion == 1 {
		result := make([]byte, 0, 1+len(nonce)+len(sealed))
		result = append(result, 1)
		result = append(result, nonce...)
		result = append(result, sealed[tagAt:]...)
		result = append(result, sealed[:tagAt]...)
		return result, nil
	}
	keyID := []byte(key.id)
	result := make([]byte, 0, 2+len(keyID)+len(nonce)+len(sealed))
	result = append(result, 2, byte(len(keyID)))
	result = append(result, keyID...)
	result = append(result, nonce...)
	result = append(result, sealed[tagAt:]...)
	result = append(result, sealed[:tagAt]...)
	return result, nil
}

func (runtime *mcpTaskRuntime) decryptPayload(value []byte, taskID, field string) ([]byte, error) {
	if len(value) < 30 {
		return nil, errors.New("invalid encrypted MCP task payload")
	}
	var offset int
	var candidates []mcpTaskKey
	switch value[0] {
	case 1:
		offset = 1
		candidates = runtime.taskKeys
	case 2:
		keyIDLength := int(value[1])
		if keyIDLength < 1 || keyIDLength > 64 || len(value) < 2+keyIDLength+28 {
			return nil, errors.New("invalid encrypted MCP task payload")
		}
		keyID := string(value[2 : 2+keyIDLength])
		if !taskKeyIDPattern.MatchString(keyID) {
			return nil, errors.New("invalid encrypted MCP task payload")
		}
		for _, key := range runtime.taskKeys {
			if key.id == keyID {
				candidates = append(candidates, key)
			}
		}
		if len(candidates) == 0 {
			return nil, errors.New("unknown MCP task payload key ID")
		}
		offset = 2 + keyIDLength
	default:
		return nil, errors.New("invalid encrypted MCP task payload")
	}
	if len(value) < offset+28 {
		return nil, errors.New("invalid encrypted MCP task payload")
	}
	nonce := value[offset : offset+12]
	sealed := append(append([]byte(nil), value[offset+28:]...), value[offset+12:offset+28]...)
	aad := []byte("gregale-mcp-task:v1:" + runtime.namespace + ":" + taskID + ":" + field)
	for _, key := range candidates {
		block, err := aes.NewCipher(key.payloadKey)
		if err != nil {
			return nil, err
		}
		aead, err := cipher.NewGCM(block)
		if err != nil {
			return nil, err
		}
		candidateAAD := aad
		if key.mainAAD && value[0] == 2 {
			candidateAAD = append(append([]byte(nil), aad...), []byte(":key:"+key.id)...)
		}
		plain, err := aead.Open(nil, nonce, sealed, candidateAAD)
		if err == nil {
			return plain, nil
		}
	}
	return nil, errors.New("could not decrypt MCP task payload with configured keys")
}

func (runtime *mcpTaskRuntime) decrypt(value []byte, taskID, field string, out any) error {
	plain, err := runtime.decryptPayload(value, taskID, field)
	if err != nil {
		return err
	}
	return json.Unmarshal(plain, out)
}

func (runtime *mcpTaskRuntime) create(ctx context.Context, name string, args map[string]any, identity *authContext, clientCapabilities map[string]any, outputSchemas ...map[string]any) (map[string]any, error) {
	runtime.drainMu.Lock()
	draining := !runtime.drainStarted.IsZero()
	runtime.drainMu.Unlock()
	if draining {
		return nil, errors.New("MCP task runtime is draining")
	}
	registered, ok := runtime.taskHandlers[name]
	if !ok {
		return nil, errors.New("task tool is not registered")
	}
	if args == nil {
		args = map[string]any{}
	}
	if err := registered.definition.Validate(args); err != nil {
		return nil, err
	}
	var outputSchema any
	if len(outputSchemas) > 0 && outputSchemas[0] != nil {
		if _, err := compileMCPToolSchema(map[string]any{"outputSchema": outputSchemas[0]}, "outputSchema"); err != nil {
			return nil, err
		}
		encoded, err := json.Marshal(outputSchemas[0])
		if err != nil {
			return nil, errors.New("invalid MCP Task output schema")
		}
		outputSchema = string(encoded)
	}
	id, err := newTaskUUID()
	if err != nil {
		return nil, err
	}
	encoded, err := runtime.encrypt(args, id, "arguments", 256*1024)
	if err != nil {
		return nil, err
	}
	inputMethods := taskInputMethodsFor(clientCapabilities)
	owners := runtime.ownerHashes(identity)
	owner := owners[0]
	tx, err := runtime.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, taskTable+":admission:"+runtime.namespace); err != nil {
		return nil, err
	}
	if err := runtime.associateOwnersTx(ctx, tx, owners); err != nil {
		return nil, err
	}
	var total, owned int
	countQuery := fmt.Sprintf(`SELECT COUNT(*)::int, COUNT(*) FILTER (WHERE %s)::int FROM gregale_mcp_tasks WHERE namespace=$1 AND expires_at>clock_timestamp() AND status IN ('queued','running','input_required')`, taskOwnerHashPredicate(2, len(owners)))
	countParams := append([]any{runtime.namespace}, taskOwnerHashParams(owners)...)
	if err := tx.QueryRow(ctx, countQuery, countParams...).Scan(&total, &owned); err != nil {
		return nil, err
	}
	if total >= runtime.maxOutstanding || owned >= runtime.maxPerOwner {
		return nil, errors.New("MCP task queue capacity reached; retry after outstanding tasks finish")
	}
	var created, updated, expires time.Time
	err = tx.QueryRow(ctx, `INSERT INTO gregale_mcp_tasks(namespace,task_id,owner_hash,tool_name,handler_version,arguments_encrypted,input_methods,status,expires_at,output_schema)
		VALUES($1,$2::uuid,$3,$4,$5,$6,$7::text[],'queued',clock_timestamp()+($8::double precision*interval '1 millisecond'),$9::jsonb)
		RETURNING created_at,updated_at,expires_at`, runtime.namespace, id, owner, name, registered.definition.Version, encoded, inputMethods, runtime.ttl.Milliseconds(), outputSchema).Scan(&created, &updated, &expires)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return map[string]any{"resultType": "task", "taskId": id, "status": "working", "statusMessage": "Task accepted for processing.", "createdAt": created.UTC().Format(time.RFC3339Nano), "lastUpdatedAt": updated.UTC().Format(time.RFC3339Nano), "ttlMs": expires.Sub(created).Milliseconds(), "pollIntervalMs": runtime.pollInterval.Milliseconds()}, nil
}

func (runtime *mcpTaskRuntime) get(ctx context.Context, id string, identity *authContext) (map[string]any, *durableMCPTask, error) {
	if !taskIDPattern.MatchString(id) {
		return nil, nil, pgx.ErrNoRows
	}
	task, err := runtime.readTask(ctx, id, identity)
	if err != nil {
		return nil, nil, err
	}
	return runtime.wire(task), task, nil
}

func (runtime *mcpTaskRuntime) readTask(ctx context.Context, id string, identity *authContext) (*durableMCPTask, error) {
	var task durableMCPTask
	var args, result, taskError, inputState []byte
	var inputMethods []string
	var cancelAt *time.Time
	owners := runtime.ownerHashes(identity)
	if err := runtime.associateOwners(ctx, owners); err != nil {
		return nil, err
	}
	params := append([]any{runtime.namespace, id}, taskOwnerHashParams(owners)...)
	query := fmt.Sprintf(`SELECT task_id::text,tool_name,handler_version,status,created_at,updated_at,expires_at,attempt_count,COALESCE(lease_token::text,''),cancel_requested_at,arguments_encrypted,result_encrypted,error_encrypted,input_state_encrypted,input_methods
		FROM gregale_mcp_tasks WHERE namespace=$1 AND task_id=$2::uuid AND %s AND expires_at>clock_timestamp()`, taskOwnerHashPredicate(3, len(owners)))
	err := runtime.pool.QueryRow(ctx, query, params...).Scan(&task.ID, &task.ToolName, &task.HandlerVersion, &task.Status, &task.CreatedAt, &task.UpdatedAt, &task.ExpiresAt, &task.AttemptCount, &task.LeaseToken, &cancelAt, &args, &result, &taskError, &inputState, &inputMethods)
	if err != nil {
		return nil, err
	}
	task.CancelAt = cancelAt
	task.InputMethods = inputMethods
	if len(args) > 0 {
		if err := runtime.decrypt(args, task.ID, "arguments", &task.Arguments); err != nil {
			return nil, err
		}
	}
	if len(result) > 0 {
		if err := runtime.decrypt(result, task.ID, "result", &task.Result); err != nil {
			return nil, err
		}
	}
	if len(taskError) > 0 {
		if err := runtime.decrypt(taskError, task.ID, "error", &task.Error); err != nil {
			return nil, err
		}
	}
	if len(inputState) > 0 {
		state := taskInputState{}
		if err := runtime.decrypt(inputState, task.ID, "input-state", &state); err != nil {
			return nil, err
		}
		task.InputRequests = outstandingTaskInputRequests(state)
	}
	return &task, nil
}

func (runtime *mcpTaskRuntime) wire(task *durableMCPTask) map[string]any {
	status := task.Status
	if status == "queued" || status == "running" {
		status = "working"
	}
	out := map[string]any{"resultType": "complete", "taskId": task.ID, "status": status, "createdAt": task.CreatedAt.UTC().Format(time.RFC3339Nano), "lastUpdatedAt": task.UpdatedAt.UTC().Format(time.RFC3339Nano), "ttlMs": task.ExpiresAt.Sub(task.CreatedAt).Milliseconds(), "pollIntervalMs": runtime.pollInterval.Milliseconds()}
	if status == "completed" {
		out["result"] = task.Result
	}
	if status == "failed" {
		out["error"] = task.Error
		if task.Error == nil {
			out["error"] = map[string]any{"code": -32603, "message": "Task execution failed"}
		}
	}
	if status == "input_required" {
		out["inputRequests"] = task.InputRequests
	}
	return out
}

func taskInputMethodsFor(capabilities map[string]any) []string {
	methods := make([]string, 0, 4)
	if elicitation, ok := capabilities["elicitation"].(map[string]any); ok {
		if capabilityEnabled(elicitation["form"]) || len(elicitation) == 0 {
			methods = append(methods, "elicitation/create:form")
		}
		if capabilityEnabled(elicitation["url"]) {
			methods = append(methods, "elicitation/create:url")
		}
	}
	if _, ok := capabilities["sampling"].(map[string]any); ok {
		methods = append(methods, "sampling/createMessage")
	}
	if _, ok := capabilities["roots"].(map[string]any); ok {
		methods = append(methods, "roots/list")
	}
	return methods
}

func capabilityEnabled(value any) bool {
	switch value := value.(type) {
	case nil:
		return false
	case bool:
		return value
	case string:
		return value != ""
	case float64:
		return value != 0
	default:
		return true
	}
}

func outstandingTaskInputRequests(state taskInputState) map[string]any {
	requests := make(map[string]any)
	for key, request := range state.Requests {
		if _, ok := state.Responses[key]; !ok {
			requests[key] = request
		}
	}
	return requests
}

func taskElicitationMode(params map[string]any) string {
	if _, present := params["mode"]; !present {
		return "form"
	}
	mode, _ := params["mode"].(string)
	return mode
}

func validateTaskInputRequests(requests map[string]any) error {
	if len(requests) < 1 || len(requests) > 64 {
		return errors.New("MCP task input request batches must contain between 1 and 64 requests")
	}
	for key, value := range requests {
		if key == "" || taskStringLength(key) > 128 {
			return errors.New("MCP task input request keys must contain between 1 and 128 characters")
		}
		request, ok := value.(map[string]any)
		if !ok {
			return errors.New("MCP task input requests must use a supported server-to-client MCP method")
		}
		method, _ := request["method"].(string)
		params, paramsOK := request["params"].(map[string]any)
		if !paramsOK || (method != "elicitation/create" && method != "sampling/createMessage" && method != "roots/list") {
			return errors.New("MCP task input requests must use a supported server-to-client MCP method")
		}
		if method == "elicitation/create" {
			mode := taskElicitationMode(params)
			if mode != "form" && mode != "url" {
				return errors.New("MCP elicitation task requests must declare form or url mode")
			}
		}
	}
	return nil
}

// requestTaskInputs persists stable client requests and returns saved responses
// when a resumed handler replays. A false ready result means the task has been
// parked in input_required and its lease has been released.
func (runtime *mcpTaskRuntime) requestTaskInputs(ctx context.Context, task *durableMCPTask, requests map[string]any) (map[string]any, bool, error) {
	if err := validateTaskInputRequests(requests); err != nil {
		return nil, false, err
	}
	for _, value := range requests {
		request := value.(map[string]any)
		method, _ := request["method"].(string)
		capability := method
		if method == "elicitation/create" {
			capability += ":" + taskElicitationMode(request["params"].(map[string]any))
		}
		if !containsString(task.InputMethods, capability) {
			return nil, false, fmt.Errorf("MCP task client did not declare support for %s", method)
		}
	}

	tx, err := runtime.pool.Begin(ctx)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var encrypted []byte
	var cancelAt *time.Time
	err = tx.QueryRow(ctx, `SELECT input_state_encrypted,cancel_requested_at FROM gregale_mcp_tasks
		WHERE namespace=$1 AND task_id=$2::uuid AND lease_token=$3::uuid AND status='running' AND lease_expires_at>clock_timestamp() AND expires_at>clock_timestamp() FOR UPDATE`, runtime.namespace, task.ID, task.LeaseToken).Scan(&encrypted, &cancelAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, errTaskInputLeaseLost
	}
	if err != nil {
		return nil, false, err
	}
	if cancelAt != nil {
		return nil, false, errTaskInputCancelled
	}
	state := taskInputState{Requests: map[string]any{}, Responses: map[string]any{}}
	if len(encrypted) > 0 {
		if err := runtime.decrypt(encrypted, task.ID, "input-state", &state); err != nil {
			return nil, false, err
		}
		if state.Requests == nil {
			state.Requests = map[string]any{}
		}
		if state.Responses == nil {
			state.Responses = map[string]any{}
		}
	}
	for key, request := range requests {
		if previous, ok := state.Requests[key]; ok {
			previousJSON, previousErr := json.Marshal(previous)
			requestJSON, requestErr := json.Marshal(request)
			if previousErr != nil || requestErr != nil || string(previousJSON) != string(requestJSON) {
				return nil, false, errors.New("MCP task input request keys cannot be reused for different requests")
			}
		} else {
			state.Requests[key] = request
		}
	}
	if len(state.Requests) > 64 {
		return nil, false, errors.New("MCP task exceeds the 64 unique input request limit")
	}
	responses := make(map[string]any, len(requests))
	ready := true
	for key := range requests {
		if response, ok := state.Responses[key]; ok {
			responses[key] = response
		} else {
			ready = false
		}
	}
	if !ready {
		encoded, err := runtime.encrypt(state, task.ID, "input-state", maxTaskInputStateBytes)
		if err != nil {
			return nil, false, err
		}
		command, updateErr := tx.Exec(ctx, `UPDATE gregale_mcp_tasks SET status='input_required',input_state_encrypted=$4,lease_token=NULL,lease_expires_at=NULL,updated_at=clock_timestamp()
			WHERE namespace=$1 AND task_id=$2::uuid AND lease_token=$3::uuid AND status='running' AND lease_expires_at>clock_timestamp() AND expires_at>clock_timestamp()`, runtime.namespace, task.ID, task.LeaseToken, encoded)
		if updateErr != nil {
			return nil, false, updateErr
		}
		if command.RowsAffected() != 1 {
			return nil, false, errTaskInputLeaseLost
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, false, err
	}
	return responses, ready, nil
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func taskStringLength(value string) int {
	return len(utf16.Encode([]rune(value)))
}

func (runtime *mcpTaskRuntime) updateInputs(ctx context.Context, id string, identity *authContext, responses map[string]any) error {
	if !taskIDPattern.MatchString(id) {
		return pgx.ErrNoRows
	}
	id = strings.ToLower(id)
	if len(responses) > 64 {
		return fmt.Errorf("%w: response count exceeds 64", errInvalidMCPTaskInput)
	}
	for key := range responses {
		if key == "" || taskStringLength(key) > 128 {
			return fmt.Errorf("%w: response keys must contain between 1 and 128 characters", errInvalidMCPTaskInput)
		}
	}
	responseJSON, err := json.Marshal(responses)
	if err != nil {
		return fmt.Errorf("%w: responses must be JSON values", errInvalidMCPTaskInput)
	}
	if len(responseJSON) > maxTaskInputStateBytes {
		return fmt.Errorf("%w: response payload exceeds 256 KiB", errInvalidMCPTaskInput)
	}
	tx, err := runtime.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var status string
	var encrypted []byte
	owners := runtime.ownerHashes(identity)
	if err := runtime.associateOwnersTx(ctx, tx, owners); err != nil {
		return err
	}
	params := append([]any{runtime.namespace, id}, taskOwnerHashParams(owners)...)
	query := fmt.Sprintf(`SELECT status,input_state_encrypted FROM gregale_mcp_tasks WHERE namespace=$1 AND task_id=$2::uuid AND %s AND expires_at>clock_timestamp() FOR UPDATE`, taskOwnerHashPredicate(3, len(owners)))
	err = tx.QueryRow(ctx, query, params...).Scan(&status, &encrypted)
	if errors.Is(err, pgx.ErrNoRows) {
		return errMCPTaskNotFound
	}
	if err != nil {
		return err
	}
	if status != "input_required" || len(encrypted) == 0 {
		return tx.Commit(ctx)
	}
	state := taskInputState{Requests: map[string]any{}, Responses: map[string]any{}}
	if err := runtime.decrypt(encrypted, id, "input-state", &state); err != nil {
		return err
	}
	changed := false
	for key, response := range responses {
		if _, known := state.Requests[key]; !known {
			continue
		}
		if _, alreadyAnswered := state.Responses[key]; alreadyAnswered {
			continue
		}
		state.Responses[key] = response
		changed = true
	}
	if !changed {
		return tx.Commit(ctx)
	}
	pending := len(outstandingTaskInputRequests(state)) > 0
	updated, err := runtime.encrypt(state, id, "input-state", maxTaskInputStateBytes)
	if err != nil {
		if strings.Contains(err.Error(), "too large") {
			return fmt.Errorf("%w: accumulated input state exceeds 256 KiB", errInvalidMCPTaskInput)
		}
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE gregale_mcp_tasks SET input_state_encrypted=$3,status=CASE WHEN $4::boolean THEN 'input_required' ELSE 'queued' END,resume_pending=NOT $4::boolean,updated_at=clock_timestamp() WHERE namespace=$1 AND task_id=$2::uuid AND status='input_required'`, runtime.namespace, id, updated, pending)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (runtime *mcpTaskRuntime) cancelTask(ctx context.Context, id string, identity *authContext) error {
	if !taskIDPattern.MatchString(id) {
		return pgx.ErrNoRows
	}
	owners := runtime.ownerHashes(identity)
	if err := runtime.associateOwners(ctx, owners); err != nil {
		return err
	}
	params := append([]any{runtime.namespace, id}, taskOwnerHashParams(owners)...)
	query := fmt.Sprintf(`UPDATE gregale_mcp_tasks SET status=CASE WHEN status IN ('queued','input_required') THEN 'cancelled' ELSE status END,
		cancel_requested_at=CASE WHEN status IN ('queued','running','input_required') THEN COALESCE(cancel_requested_at,clock_timestamp()) ELSE cancel_requested_at END,
		updated_at=CASE WHEN status IN ('queued','running','input_required') THEN clock_timestamp() ELSE updated_at END,
		lease_token=CASE WHEN status IN ('queued','input_required') THEN NULL ELSE lease_token END,
		lease_expires_at=CASE WHEN status IN ('queued','input_required') THEN NULL ELSE lease_expires_at END
		WHERE namespace=$1 AND task_id=$2::uuid AND %s AND expires_at>clock_timestamp()`, taskOwnerHashPredicate(3, len(owners)))
	command, err := runtime.pool.Exec(ctx, query, params...)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

func taskPayloadNeedsRewrapSQL(column string) string {
	return fmt.Sprintf(`(%[1]s IS NOT NULL AND CASE
		WHEN octet_length(%[1]s) < 2 THEN TRUE
		WHEN get_byte(%[1]s,0) <> 2 THEN TRUE
		WHEN get_byte(%[1]s,1) = 0 OR get_byte(%[1]s,1) > 64 THEN TRUE
		WHEN octet_length(%[1]s) < 2 + get_byte(%[1]s,1) + 28 THEN TRUE
		ELSE substring(%[1]s FROM 3 FOR get_byte(%[1]s,1)) <> $2::bytea
	END)`, column)
}

func (runtime *mcpTaskRuntime) rewrapBatch(ctx context.Context, limit int) (int, error) {
	runtime.rewrapMu.Lock()
	defer runtime.rewrapMu.Unlock()
	if limit < 1 {
		return 0, nil
	}
	if runtime.payloadEncryptionVersion != 2 {
		return 0, nil
	}
	if len(runtime.taskKeys) == 0 {
		return 0, errors.New("MCP task encryption keys are unavailable")
	}
	if runtime.ambiguousRewrapKeyID() {
		// A retained row can carry several megabytes of encrypted fields.
		limit = min(limit, 8)
	}
	tx, err := runtime.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT set_config('gregale.mcp_task_rewrap','on',true)`); err != nil {
		return 0, err
	}
	rows, through, scanning, err := runtime.rewrapCandidates(ctx, tx, limit)
	if err != nil {
		return 0, err
	}
	type encryptedTask struct {
		id                                  string
		args, result, taskError, inputState []byte
	}
	var batch []encryptedTask
	for rows.Next() {
		var item encryptedTask
		if err := rows.Scan(&item.id, &item.args, &item.result, &item.taskError, &item.inputState); err != nil {
			rows.Close()
			return 0, err
		}
		batch = append(batch, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()
	changed := 0
	for _, item := range batch {
		needsUpdate := false
		rotate := func(value []byte, field string, maxBytes int) ([]byte, error) {
			encrypted, updated, err := runtime.rewrapPayload(value, item.id, field, maxBytes)
			needsUpdate = needsUpdate || updated
			return encrypted, err
		}
		args, err := rotate(item.args, "arguments", 256*1024)
		if err != nil {
			return 0, err
		}
		result, err := rotate(item.result, "result", 1024*1024)
		if err != nil {
			return 0, err
		}
		taskError, err := rotate(item.taskError, "error", 1024*1024)
		if err != nil {
			return 0, err
		}
		inputState, err := rotate(item.inputState, "input-state", maxTaskInputStateBytes)
		if err != nil {
			return 0, err
		}
		if !needsUpdate {
			continue
		}
		if _, err := tx.Exec(ctx, `UPDATE gregale_mcp_tasks SET arguments_encrypted=$3,result_encrypted=$4,error_encrypted=$5,input_state_encrypted=$6 WHERE namespace=$1 AND task_id=$2::uuid`, runtime.namespace, item.id, args, result, taskError, inputState); err != nil {
			return 0, err
		}
		changed++
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	if scanning {
		runtime.rewrapAfter, runtime.rewrapThrough = "", ""
		if len(batch) == limit {
			runtime.rewrapAfter, runtime.rewrapThrough = batch[len(batch)-1].id, through
		}
	}
	return changed, nil
}

func (runtime *mcpTaskRuntime) rewrapLoop(ctx context.Context) {
	defer close(runtime.rewrapDone)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	lastErrorLog := time.Time{}
	for {
		batchCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		_, err := runtime.rewrapBatch(batchCtx, 64)
		cancel()
		if err != nil && ctx.Err() == nil && (lastErrorLog.IsZero() || time.Since(lastErrorLog) >= time.Minute) {
			log.Printf("MCP task key rewrap batch failed")
			lastErrorLog = time.Now()
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (runtime *mcpTaskRuntime) workerLoop(ctx context.Context) {
	defer close(runtime.done)
	ticker := time.NewTicker(runtime.pollInterval)
	defer ticker.Stop()
	capabilityTicker := time.NewTicker(10 * time.Second)
	defer capabilityTicker.Stop()
	semaphore := make(chan struct{}, runtime.concurrency)
	lastCleanup := time.Now()
	lastCapabilityCleanup := time.Now()
	refreshCapabilities := func() {
		heartbeatCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if err := runtime.heartbeatWorkerCapabilities(heartbeatCtx); err != nil && ctx.Err() == nil {
			log.Printf("MCP task worker capability heartbeat failed")
		}
	}
	refreshCapabilities()
	for {
		if ctx.Err() != nil {
			for len(semaphore) > 0 {
				time.Sleep(10 * time.Millisecond)
			}
			return
		}
		if time.Since(lastCleanup) >= time.Minute {
			_ = runtime.cleanupExpiredTasks(ctx)
			lastCleanup = time.Now()
		}
		if time.Since(lastCapabilityCleanup) >= time.Hour {
			_, _ = runtime.pool.Exec(ctx, `DELETE FROM gregale_mcp_task_worker_handlers WHERE namespace=$1 AND heartbeat_at<clock_timestamp()-interval '1 day'`, runtime.namespace)
			_, _ = runtime.pool.Exec(ctx, `DELETE FROM gregale_mcp_task_workers WHERE namespace=$1 AND expires_at<=clock_timestamp() AND NOT draining`, runtime.namespace)
			lastCapabilityCleanup = time.Now()
		}
		for ctx.Err() == nil && len(semaphore) < cap(semaphore) {
			task, err := runtime.claim(ctx)
			if err != nil || task == nil {
				break
			}
			runtime.drainMu.Lock()
			if !runtime.drainStarted.IsZero() || !task.leaseDeadline.IsZero() && !time.Now().Before(task.leaseDeadline) {
				runtime.drainMu.Unlock()
				runtime.releaseDrainedTask(ctx, task)
				break
			}
			runtime.activeTasks[task.ID] = task
			runtime.drainMu.Unlock()
			semaphore <- struct{}{}
			go func(task *durableMCPTask) {
				defer func() {
					runtime.drainMu.Lock()
					delete(runtime.activeTasks, task.ID)
					runtime.drainMu.Unlock()
					<-semaphore
				}()
				runtime.runTask(runtime.executionCtx, task)
			}(task)
		}
		select {
		case <-ctx.Done():
			for len(semaphore) > 0 {
				time.Sleep(10 * time.Millisecond)
			}
			return
		case <-ticker.C:
		case <-capabilityTicker.C:
			refreshCapabilities()
		}
	}
}

func (runtime *mcpTaskRuntime) heartbeatWorkerCapabilities(ctx context.Context) error {
	runtime.workerRegistryMu.Lock()
	defer runtime.workerRegistryMu.Unlock()
	runtime.drainMu.Lock()
	draining := !runtime.drainStarted.IsZero()
	runtime.drainMu.Unlock()
	if draining {
		return nil
	}
	if len(runtime.taskExecutors) == 0 {
		return nil
	}
	toolNames := make([]string, 0, len(runtime.taskExecutors))
	handlerVersions := make([]string, 0, len(runtime.taskExecutors))
	for handler := range runtime.taskExecutors {
		toolNames = append(toolNames, handler.toolName)
		handlerVersions = append(handlerVersions, handler.version)
	}
	tx, err := runtime.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	handlers := make([]map[string]string, 0, len(toolNames))
	for index, name := range toolNames {
		handlers = append(handlers, map[string]string{"name": name, "version": handlerVersions[index]})
	}
	encoded, err := json.Marshal(handlers)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO gregale_mcp_task_workers(namespace,worker_id,handlers,heartbeat_at,expires_at)
		VALUES($1,$2::uuid,$3::jsonb,clock_timestamp(),clock_timestamp()+interval '90 seconds')
		ON CONFLICT(namespace,worker_id) DO UPDATE SET handlers=EXCLUDED.handlers,heartbeat_at=EXCLUDED.heartbeat_at,expires_at=EXCLUDED.expires_at`, runtime.namespace, runtime.workerID, encoded); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO gregale_mcp_task_worker_handlers(namespace,worker_id,tool_name,handler_version,deployment_id,heartbeat_at)
		SELECT $1,$2::uuid,handler.tool_name,handler.handler_version,$5,clock_timestamp()
		  FROM unnest($3::text[],$4::text[]) AS handler(tool_name,handler_version)
		ON CONFLICT (namespace,worker_id,tool_name,handler_version)
		DO UPDATE SET deployment_id=EXCLUDED.deployment_id,heartbeat_at=EXCLUDED.heartbeat_at
	`, runtime.namespace, runtime.workerID, toolNames, handlerVersions, runtime.deploymentID)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (runtime *mcpTaskRuntime) runTask(parent context.Context, task *durableMCPTask) {
	if parent.Err() != nil {
		return
	}
	execute := runtime.taskExecutors[mcpTaskHandlerKey{toolName: task.ToolName, version: task.HandlerVersion}]
	if execute == nil {
		runtime.failTask(parent, task)
		return
	}
	validateOutput, err := compileMCPTaskOutput(task.OutputSchema)
	if err != nil {
		runtime.failTask(parent, task)
		return
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	finished := make(chan struct{})
	abortReason := make(chan string, 1)
	loseLease := func() {
		select {
		case abortReason <- "lease_lost":
		default:
		}
		cancel()
	}
	lease := newMCPTaskLeaseDeadline(ctx, task.leaseDeadline, loseLease)
	defer lease.close()
	go func() {
		defer close(finished)
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				startedAt := time.Now()
				cancelRequested, err := runtime.heartbeatTask(ctx, task)
				if errors.Is(err, pgx.ErrNoRows) {
					loseLease()
					return
				}
				if err != nil {
					continue
				}
				if cancelRequested {
					select {
					case abortReason <- "cancelled":
					default:
					}
					cancel()
					return
				}
				lease.renew(startedAt)
			}
		}
	}()
	execution := &MCPTaskExecutionContext{TaskID: task.ID}
	execution.requestInputs = func(requests map[string]any) (map[string]any, error) {
		responses, ready, err := runtime.requestTaskInputs(ctx, task, requests)
		if err != nil {
			return nil, err
		}
		if !ready {
			return nil, ErrMCPTaskInputRequired
		}
		return responses, nil
	}
	result, executeErr := invokeMCPTaskExecutor(execute, ctx, task.Arguments, execution)
	lease.close()
	cancel()
	<-finished
	if parent.Err() != nil {
		return
	}
	select {
	case reason := <-abortReason:
		if reason == "lease_lost" {
			return
		}
		if reason == "cancelled" {
			runtime.finishCancelled(parent, task)
			return
		}
	default:
	}
	if errors.Is(executeErr, errTaskInputLeaseLost) {
		return
	}
	if errors.Is(executeErr, errTaskInputCancelled) {
		runtime.finishCancelled(parent, task)
		return
	}
	if errors.Is(executeErr, ErrMCPTaskInputRequired) {
		return
	}
	if executeErr != nil || result == nil {
		runtime.failTaskWithRetry(parent, task, errors.Is(executeErr, ErrRetryableMCPTask))
		return
	}
	encodedResult, err := json.Marshal(result)
	if err != nil || validateOutput(encodedResult) != nil {
		runtime.failTask(parent, task)
		return
	}
	encrypted, err := runtime.encrypt(result, task.ID, "result", 1024*1024)
	if err == nil {
		_, err = runtime.pool.Exec(parent, taskLeaseInstantSQL+`UPDATE gregale_mcp_tasks SET status=CASE WHEN cancel_requested_at IS NULL THEN 'completed' ELSE 'cancelled' END,result_encrypted=CASE WHEN cancel_requested_at IS NULL THEN $4::bytea ELSE NULL END,error_encrypted=NULL,updated_at=clock_timestamp(),lease_token=NULL,lease_expires_at=NULL FROM instant WHERE namespace=$1 AND task_id=$2::uuid AND lease_token=$3::uuid AND status='running' AND lease_expires_at>instant.now AND expires_at>instant.now`, runtime.namespace, task.ID, task.LeaseToken, encrypted)
	}
	if err != nil {
		runtime.failTask(parent, task)
	}
}

func validateBuildReportTask(args map[string]any) error {
	report, reportOK := args["report"].(string)
	steps, stepsOK := args["steps"].(float64)
	_, approvalPresent := args["request_approval"].(bool)
	if (len(args) != 2 && len(args) != 3) || (len(args) == 3 && !approvalPresent) || !reportOK || strings.TrimSpace(report) == "" || taskStringLength(report) > 80 || !stepsOK || steps != float64(int(steps)) || steps < 1 || steps > 20 {
		return errors.New("report must be 1 to 80 characters and steps must be an integer from 1 to 20")
	}
	return nil
}

func executeLegacyBuildReportTask(ctx context.Context, args map[string]any, _ *MCPTaskExecutionContext) (map[string]any, error) {
	return executeBuildReportSteps(ctx, args)
}

func executeBuildReportTask(ctx context.Context, args map[string]any, execution *MCPTaskExecutionContext) (map[string]any, error) {
	report, _ := args["report"].(string)
	steps := int(args["steps"].(float64))
	approved := true
	if args["request_approval"] == true {
		response, err := execution.RequestInput("approval", map[string]any{
			"method": "elicitation/create",
			"params": map[string]any{
				"mode": "form", "message": "Approve generating this report?",
				"requestedSchema": map[string]any{
					"type": "object", "properties": map[string]any{"approved": map[string]any{"type": "boolean", "title": "Approve report generation"}},
					"required": []string{"approved"},
				},
			},
		})
		if err != nil {
			return nil, err
		}
		approval, _ := response.(map[string]any)
		content, _ := approval["content"].(map[string]any)
		approved = approval["action"] == "accept" && content["approved"] == true
	}
	if approved {
		if err := waitBuildReportSteps(ctx, steps); err != nil {
			return nil, err
		}
	}
	text := fmt.Sprintf("Report %s is ready after %d steps.", report, steps)
	if !approved {
		text = "Report was not approved."
	}
	return map[string]any{"content": []map[string]string{{"type": "text", "text": text}}}, nil
}

func executeBuildReportSteps(ctx context.Context, args map[string]any) (map[string]any, error) {
	report, _ := args["report"].(string)
	steps, _ := args["steps"].(float64)
	if err := waitBuildReportSteps(ctx, int(steps)); err != nil {
		return nil, err
	}
	return map[string]any{"content": []map[string]string{{"type": "text", "text": fmt.Sprintf("Report %s is ready after %d steps.", report, int(steps))}}}, nil
}

func waitBuildReportSteps(ctx context.Context, steps int) error {
	for step := 0; step < steps; step++ {
		timer := time.NewTimer(200 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return nil
}

func newTaskUUID() (string, error) {
	value := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, value); err != nil {
		return "", err
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", value[0:4], value[4:6], value[6:8], value[8:10], value[10:16]), nil
}

func (runtime *mcpTaskRuntime) finishCancelled(ctx context.Context, task *durableMCPTask) {
	_, _ = runtime.pool.Exec(ctx, taskLeaseInstantSQL+`UPDATE gregale_mcp_tasks SET status='cancelled',result_encrypted=NULL,error_encrypted=NULL,updated_at=clock_timestamp(),lease_token=NULL,lease_expires_at=NULL FROM instant WHERE namespace=$1 AND task_id=$2::uuid AND lease_token=$3::uuid AND status='running' AND lease_expires_at>instant.now AND expires_at>instant.now`, runtime.namespace, task.ID, task.LeaseToken)
}

func (runtime *mcpTaskRuntime) failTask(ctx context.Context, task *durableMCPTask) {
	runtime.failTaskWithRetry(ctx, task, false)
}

func taskURIID(uri string) (string, bool) {
	const prefix = "task://tasks/"
	if !strings.HasPrefix(uri, prefix) {
		return "", false
	}
	id := strings.TrimPrefix(uri, prefix)
	if !taskIDPattern.MatchString(id) {
		return "", false
	}
	return id, true
}

func taskResourceTemplate() map[string]any {
	return map[string]any{"uriTemplate": "task://tasks/{taskId}", "name": "task", "title": "MCP task", "description": "Read the current state and result of a task owned by this caller.", "mimeType": "application/json"}
}
