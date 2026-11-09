package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

const defaultGregaleAPIURL = "https://api.gregale.dev"

var mcpTaskAppSlugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

type mcpTaskMetricsSettings struct {
	appSlug string
	token   string
	apiURL  string
}

type mcpTaskQueueMetrics struct {
	outstandingTasks                                                   int64
	oldestAgeSeconds                                                   float64
	withoutCompatibleWorkerTasks                                       int64
	runningTasks, capacityWaitingTasks, failedTasks, retryWaitingTasks int64
	activeWorkers, drainingWorkers, unsupportedHandlerTasks            int64
	handlerCompatibility                                               []mcpTaskHandlerCompatibility
	compatibilityTruncated                                             bool
}

type mcpTaskHandlerCompatibility struct {
	ToolName                 string                            `json:"tool_name"`
	HandlerVersion           string                            `json:"handler_version"`
	WaitingTasks             int64                             `json:"waiting_tasks"`
	LiveCompatibleWorkers    int64                             `json:"live_compatible_workers"`
	UnknownDeploymentWorkers int64                             `json:"unknown_deployment_workers,omitempty"`
	Deployments              []mcpTaskHandlerDeploymentWorkers `json:"deployments,omitempty"`
}

type mcpTaskHandlerDeploymentWorkers struct {
	DeploymentID string `json:"deployment_id"`
	Workers      int64  `json:"workers"`
}

type mcpTaskMetricsPublisher struct {
	cancel context.CancelFunc
	done   chan struct{}
	once   sync.Once
}

func configuredMCPTaskRole() (string, error) {
	role := strings.TrimSpace(os.Getenv("MCP_TASKS_ROLE"))
	if role == "" {
		role = "combined"
	}
	if role != "combined" && role != "web" && role != "worker" && role != "observer" {
		return "", errors.New("MCP_TASKS_ROLE must be combined, web, worker, or observer")
	}
	return role, nil
}

func resolveMCPTaskMetricsSettings(role string) (*mcpTaskMetricsSettings, error) {
	appSlug := strings.TrimSpace(os.Getenv("MCP_TASKS_SCALING_APP_SLUG"))
	token := strings.TrimSpace(os.Getenv("MCP_TASKS_SCALING_TOKEN"))
	if appSlug == "" && token == "" {
		return nil, nil
	}
	if appSlug == "" || token == "" {
		return nil, errors.New("MCP task scaling requires both MCP_TASKS_SCALING_APP_SLUG and MCP_TASKS_SCALING_TOKEN")
	}
	if role != "worker" && role != "observer" {
		return nil, errors.New("MCP task scaling metrics can only be published by the worker role or observer")
	}
	if !mcpTaskAppSlugPattern.MatchString(appSlug) {
		return nil, errors.New("invalid MCP task scaling app slug")
	}
	apiURL := strings.TrimSpace(os.Getenv("GREGALE_API_URL"))
	if apiURL == "" {
		apiURL = defaultGregaleAPIURL
	}
	parsed, err := url.Parse(apiURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("gregale metrics API URL must be HTTPS without credentials, query, or fragment")
	}
	return &mcpTaskMetricsSettings{appSlug: appSlug, token: token, apiURL: parsed.Scheme + "://" + parsed.Host}, nil
}

func startMCPTaskMetricsPublisher(runtime *mcpTaskRuntime, settings mcpTaskMetricsSettings, observer bool) *mcpTaskMetricsPublisher {
	ctx, cancel := context.WithCancel(context.Background())
	publisher := &mcpTaskMetricsPublisher{cancel: cancel, done: make(chan struct{})}
	go publisher.run(ctx, runtime, settings, observer)
	return publisher
}

func (publisher *mcpTaskMetricsPublisher) close() {
	if publisher == nil {
		return
	}
	publisher.once.Do(func() {
		publisher.cancel()
		<-publisher.done
	})
}

func (publisher *mcpTaskMetricsPublisher) run(ctx context.Context, runtime *mcpTaskRuntime, settings mcpTaskMetricsSettings, observer bool) {
	defer close(publisher.done)
	client := &http.Client{
		Timeout: 5 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	publish := func() {
		metricsContext, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		metrics, err := runtime.queueMetrics(metricsContext)
		if err == nil {
			err = publishMCPTaskGauges(metricsContext, client, settings, metrics)
		}
		if err == nil && observer {
			err = pushMCPTaskCompatibilityReport(metricsContext, client, settings, metrics.handlerCompatibility, metrics.compatibilityTruncated)
		}
		if err == nil && observer {
			err = pushMCPTaskMetric(metricsContext, client, settings, "mcp_tasks_observer_heartbeat", 1)
		}
		if err != nil {
			event := "mcp_task_metrics_publish_failed"
			if observer {
				event = "mcp_task_observer_health_check_failed"
			}
			body, _ := json.Marshal(struct {
				Event string `json:"event"`
			}{Event: event})
			log.Print(string(body))
		}
	}

	publish()
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			publish()
		}
	}
}

func publishMCPTaskGauges(ctx context.Context, client *http.Client, settings mcpTaskMetricsSettings, metrics mcpTaskQueueMetrics) error {
	for _, gauge := range []struct {
		name  string
		value any
	}{
		{"mcp_tasks_outstanding", metrics.outstandingTasks},
		{"mcp_tasks_oldest_age_seconds", metrics.oldestAgeSeconds},
		{"mcp_tasks_without_compatible_worker", metrics.withoutCompatibleWorkerTasks},
		{"mcp_tasks_running", metrics.runningTasks},
		{"mcp_tasks_capacity_waiting", metrics.capacityWaitingTasks},
		{"mcp_tasks_failed", metrics.failedTasks},
		{"mcp_tasks_retry_waiting", metrics.retryWaitingTasks},
		{"mcp_tasks_active_workers", metrics.activeWorkers},
		{"mcp_tasks_draining_workers", metrics.drainingWorkers},
		{"mcp_tasks_unsupported_handler_tasks", metrics.unsupportedHandlerTasks},
	} {
		if err := pushMCPTaskMetric(ctx, client, settings, gauge.name, gauge.value); err != nil {
			return err
		}
	}
	return nil
}

func pushMCPTaskMetric(ctx context.Context, client *http.Client, settings mcpTaskMetricsSettings, name string, value any) error {
	body, err := json.Marshal(map[string]any{"value": value})
	if err != nil {
		return err
	}
	endpoint := fmt.Sprintf("%s/v1/apps/%s/custom-metrics/%s", settings.apiURL, url.PathEscape(settings.appSlug), name)
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+settings.token)
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusNoContent {
		return fmt.Errorf("gregale custom metric push failed with HTTP %d", response.StatusCode)
	}
	return nil
}

func pushMCPTaskCompatibilityReport(ctx context.Context, client *http.Client, settings mcpTaskMetricsSettings, handlers []mcpTaskHandlerCompatibility, truncated bool) error {
	body, err := json.Marshal(struct {
		Handlers  []mcpTaskHandlerCompatibility `json:"handlers"`
		Truncated bool                          `json:"truncated"`
	}{Handlers: handlers, Truncated: truncated})
	if err != nil {
		return err
	}
	endpoint := fmt.Sprintf("%s/v1/apps/%s/mcp-task-compatibility", settings.apiURL, url.PathEscape(settings.appSlug))
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+settings.token)
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusNoContent {
		return fmt.Errorf("gregale MCP task compatibility report failed with HTTP %d", response.StatusCode)
	}
	return nil
}

func (runtime *mcpTaskRuntime) queueMetrics(ctx context.Context) (mcpTaskQueueMetrics, error) {
	if runtime.role == "observer" {
		if err := runtime.assertReadOnly(ctx); err != nil {
			return mcpTaskQueueMetrics{}, err
		}
	}
	var metrics mcpTaskQueueMetrics
	err := runtime.pool.QueryRow(ctx, mcpTaskQueueMetricsSQL, runtime.namespace, runtime.execution.maxRunning, runtime.execution.maxRunningPerOwner, runtime.execution.maxAttempts).Scan(
		&metrics.outstandingTasks, &metrics.oldestAgeSeconds, &metrics.runningTasks, &metrics.capacityWaitingTasks,
		&metrics.failedTasks, &metrics.retryWaitingTasks, &metrics.activeWorkers, &metrics.drainingWorkers,
		&metrics.unsupportedHandlerTasks, &metrics.withoutCompatibleWorkerTasks)
	if err != nil {
		return mcpTaskQueueMetrics{}, err
	}
	if metrics.outstandingTasks < 0 || metrics.oldestAgeSeconds < 0 || metrics.withoutCompatibleWorkerTasks < 0 ||
		metrics.runningTasks < 0 || metrics.capacityWaitingTasks < 0 || metrics.failedTasks < 0 || metrics.retryWaitingTasks < 0 ||
		metrics.activeWorkers < 0 || metrics.drainingWorkers < 0 || metrics.unsupportedHandlerTasks < 0 {
		return mcpTaskQueueMetrics{}, errors.New("MCP task queue metrics returned invalid values")
	}
	if runtime.role == "observer" {
		metrics.handlerCompatibility = make([]mcpTaskHandlerCompatibility, 0, 256)
		rows, err := runtime.pool.Query(ctx, `
			WITH pending AS (
				SELECT queued.namespace, queued.tool_name, queued.handler_version
				  FROM gregale_mcp_tasks AS queued
				 WHERE queued.namespace=$1 AND queued.expires_at>clock_timestamp()
				   AND queued.status IN ('queued','running','input_required')
			), waiting AS (
				SELECT tool_name, handler_version, COUNT(*)::bigint AS waiting_tasks
				  FROM pending GROUP BY tool_name, handler_version
			), limited_waiting AS (
				SELECT * FROM waiting ORDER BY waiting_tasks DESC, tool_name, handler_version LIMIT 257
			), by_deployment AS (
				SELECT waiting.tool_name, waiting.handler_version, waiting.waiting_tasks,
				       handler.deployment_id, COUNT(DISTINCT handler.worker_id)::bigint AS workers
				  FROM limited_waiting AS waiting
				  LEFT JOIN gregale_mcp_task_worker_handlers AS handler
				    ON handler.namespace=$1 AND handler.tool_name=waiting.tool_name
				   AND handler.handler_version=waiting.handler_version
				   AND handler.heartbeat_at>clock_timestamp()-interval '45 seconds'
				 GROUP BY waiting.tool_name, waiting.handler_version, waiting.waiting_tasks, handler.deployment_id
			), ranked AS (
				SELECT *,
				       SUM(workers) OVER (PARTITION BY tool_name,handler_version) AS all_workers,
				       COUNT(*) OVER (PARTITION BY tool_name,handler_version) AS deployment_count,
				       ROW_NUMBER() OVER (PARTITION BY tool_name,handler_version ORDER BY workers DESC,deployment_id) AS deployment_rank
				  FROM by_deployment
			)
			SELECT tool_name,handler_version,
			       LEAST(waiting_tasks,1000000000000::bigint)::bigint,
			       LEAST(all_workers,1000000000000::bigint)::bigint,
			       coalesce(deployment_id,''),LEAST(workers,1000000000000::bigint)::bigint,
			       deployment_count, deployment_rank
			  FROM ranked
			 WHERE deployment_rank<=64
			 ORDER BY waiting_tasks DESC,tool_name,handler_version,deployment_rank
		`, runtime.namespace)
		if err != nil {
			var postgresErr *pgconn.PgError
			if errors.As(err, &postgresErr) && postgresErr.Code == "42703" {
				// During a mixed-version rollout the read-only observer cannot
				// migrate this table itself. Publish a fail-closed snapshot until
				// a worker adds the deployment identity column.
				metrics.compatibilityTruncated = true
				return metrics, nil
			}
			return mcpTaskQueueMetrics{}, err
		}
		defer rows.Close()
		byHandler := make(map[string]int, 256)
		deploymentEntries := 0
		for rows.Next() {
			var name, version, deploymentID string
			var waitingTasks, allWorkers, workers int64
			var deploymentCount, deploymentRank int
			if err := rows.Scan(&name, &version, &waitingTasks, &allWorkers, &deploymentID, &workers, &deploymentCount, &deploymentRank); err != nil {
				return mcpTaskQueueMetrics{}, err
			}
			key := name + "\x00" + version
			index, exists := byHandler[key]
			if !exists {
				metrics.handlerCompatibility = append(metrics.handlerCompatibility, mcpTaskHandlerCompatibility{
					ToolName: name, HandlerVersion: version, WaitingTasks: waitingTasks,
					LiveCompatibleWorkers: allWorkers, UnknownDeploymentWorkers: allWorkers,
					Deployments: []mcpTaskHandlerDeploymentWorkers{},
				})
				index = len(metrics.handlerCompatibility) - 1
				byHandler[key] = index
			}
			if deploymentCount > 64 {
				metrics.compatibilityTruncated = true
			}
			if deploymentID != "" && workers > 0 && deploymentEntries < 768 {
				item := &metrics.handlerCompatibility[index]
				item.Deployments = append(item.Deployments, mcpTaskHandlerDeploymentWorkers{DeploymentID: deploymentID, Workers: workers})
				item.UnknownDeploymentWorkers -= workers
				deploymentEntries++
			} else if deploymentID != "" && workers > 0 {
				metrics.compatibilityTruncated = true
			}
		}
		if err := rows.Err(); err != nil {
			return mcpTaskQueueMetrics{}, err
		}
		if len(metrics.handlerCompatibility) > 256 {
			metrics.compatibilityTruncated = true
			metrics.handlerCompatibility = metrics.handlerCompatibility[:256]
		}
	}
	return metrics, nil
}

func (runtime *mcpTaskRuntime) assertReadOnly(ctx context.Context) error {
	var tableExists, canInsert, canUpdate, canDelete, canTruncate, canReference, canCreateTrigger, canCreateSchemas, canCreateInTaskSchema bool
	err := runtime.pool.QueryRow(ctx, `
		SELECT pg_catalog.to_regclass($1)::oid IS NOT NULL,
		       COALESCE(pg_catalog.has_table_privilege(current_user, pg_catalog.to_regclass($1)::oid, 'INSERT') OR
		                pg_catalog.has_any_column_privilege(current_user, pg_catalog.to_regclass($1)::oid, 'INSERT'), false),
		       COALESCE(pg_catalog.has_table_privilege(current_user, pg_catalog.to_regclass($1)::oid, 'UPDATE') OR
		                pg_catalog.has_any_column_privilege(current_user, pg_catalog.to_regclass($1)::oid, 'UPDATE'), false),
		       COALESCE(pg_catalog.has_table_privilege(current_user, pg_catalog.to_regclass($1)::oid, 'DELETE'), false),
		       COALESCE(pg_catalog.has_table_privilege(current_user, pg_catalog.to_regclass($1)::oid, 'TRUNCATE'), false),
		       COALESCE(pg_catalog.has_table_privilege(current_user, pg_catalog.to_regclass($1)::oid, 'REFERENCES') OR
		                pg_catalog.has_any_column_privilege(current_user, pg_catalog.to_regclass($1)::oid, 'REFERENCES'), false),
		       COALESCE(pg_catalog.has_table_privilege(current_user, pg_catalog.to_regclass($1)::oid, 'TRIGGER'), false),
		       pg_catalog.has_database_privilege(current_user, current_database(), 'CREATE'),
		       COALESCE((
		         SELECT pg_catalog.has_schema_privilege(current_user, n.nspname, 'CREATE')
		           FROM pg_catalog.pg_class AS c
		           JOIN pg_catalog.pg_namespace AS n ON n.oid = c.relnamespace
		          WHERE c.oid = pg_catalog.to_regclass($1)::oid
		       ), false)
	`, taskTable).Scan(&tableExists, &canInsert, &canUpdate, &canDelete, &canTruncate, &canReference, &canCreateTrigger, &canCreateSchemas, &canCreateInTaskSchema)
	if err != nil {
		return err
	}
	if !tableExists || canInsert || canUpdate || canDelete || canTruncate || canReference || canCreateTrigger || canCreateSchemas || canCreateInTaskSchema {
		return errors.New("MCP task observer requires read-only PostgreSQL access")
	}
	for _, table := range []string{taskWorkerHandlersTable, "gregale_mcp_task_owners", "gregale_mcp_task_workers"} {
		var handlersTableExists, handlersCanWrite bool
		err = runtime.pool.QueryRow(ctx, `
		SELECT pg_catalog.to_regclass($1)::oid IS NOT NULL,
		       COALESCE(pg_catalog.has_table_privilege(current_user,pg_catalog.to_regclass($1)::oid,'INSERT') OR pg_catalog.has_any_column_privilege(current_user,pg_catalog.to_regclass($1)::oid,'INSERT') OR
		                pg_catalog.has_table_privilege(current_user,pg_catalog.to_regclass($1)::oid,'UPDATE') OR pg_catalog.has_any_column_privilege(current_user,pg_catalog.to_regclass($1)::oid,'UPDATE') OR
		                pg_catalog.has_table_privilege(current_user,pg_catalog.to_regclass($1)::oid,'DELETE') OR pg_catalog.has_table_privilege(current_user,pg_catalog.to_regclass($1)::oid,'TRUNCATE') OR
		                pg_catalog.has_table_privilege(current_user,pg_catalog.to_regclass($1)::oid,'REFERENCES') OR pg_catalog.has_any_column_privilege(current_user,pg_catalog.to_regclass($1)::oid,'REFERENCES') OR
		                pg_catalog.has_table_privilege(current_user,pg_catalog.to_regclass($1)::oid,'TRIGGER'),false)
	`, table).Scan(&handlersTableExists, &handlersCanWrite)
		if err != nil {
			return err
		}
		if !handlersTableExists || handlersCanWrite {
			return errors.New("MCP task observer requires read-only PostgreSQL access")
		}
	}
	return nil
}
