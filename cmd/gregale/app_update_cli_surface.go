package main

// updateAppCLISurface records how each JSON-visible UpdateAppRequest field is
// reached from the customer CLI. Paths describe a flag, subcommand, or deploy
// manifest; KnownGap is an explicit review disposition for fields that do not
// currently have a customer CLI mutation path. The reflection test keeps this
// inventory in sync with the API DTO so new fields cannot silently bypass CLI
// review.
type updateAppCLISurface struct {
	Path     string
	AppFlags []string
	KnownGap string
}

var updateAppRequestCLISurfaces = map[string]updateAppCLISurface{
	"visibility":                      {Path: "gregale app <slug> --visibility", AppFlags: []string{"visibility"}},
	"allowed_service_callers":         {KnownGap: "no customer CLI setter; API-only today"},
	"allowed_service_call_scopes":     {KnownGap: "no customer CLI setter; API-only today"},
	"service_binding_targets":         {KnownGap: "no customer CLI setter; API-only today"},
	"service_reliability":             {KnownGap: "no customer CLI setter; API-only today"},
	"service_binding_policy":          {KnownGap: "no customer CLI setter; API-only today"},
	"service_binding_transport":       {KnownGap: "no customer CLI setter; API-only today"},
	"ram_mb":                          {Path: "gregale app <slug> --ram", AppFlags: []string{"ram"}},
	"cpu_millicores":                  {Path: "gregale app <slug> --cpu-millicores", AppFlags: []string{"cpu-millicores"}},
	"resource_profile":                {Path: "gregale app <slug> --profile", AppFlags: []string{"profile"}},
	"idle_timeout_s":                  {Path: "gregale app <slug> --idle", AppFlags: []string{"idle"}},
	"max_concurrency":                 {Path: "gregale app <slug> --max-concurrency", AppFlags: []string{"max-concurrency"}},
	"execution_mode":                  {Path: "gregale.yaml lifecycle.execution_mode"},
	"restart_policy":                  {Path: "gregale.yaml lifecycle.restart_policy"},
	"after_restore":                   {Path: "gregale.yaml lifecycle.after_restore"},
	"profiling":                       {Path: "gregale.yaml profiling / lifecycle.profiling"},
	"tracing":                         {Path: "gregale.yaml tracing / lifecycle.tracing"},
	"startup_deadline_s":              {Path: "gregale.yaml lifecycle.startup_deadline_s"},
	"max_retries":                     {Path: "gregale.yaml lifecycle.max_retries"},
	"stop_grace_period_s":             {Path: "gregale.yaml lifecycle.stop_grace_period_s"},
	"stop_signal":                     {Path: "gregale.yaml lifecycle.stop_signal"},
	"request_timeout_s":               {Path: "gregale app <slug> --request-timeout", AppFlags: []string{"request-timeout"}},
	"request_rate_limit_rps":          {KnownGap: "no customer CLI setter; API-only today"},
	"request_rate_limit_burst":        {KnownGap: "no customer CLI setter; API-only today"},
	"retry_policy":                    {Path: "gregale.yaml retry_policy"},
	"service_replicas":                {Path: "gregale.yaml lifecycle.service_replicas"},
	"before_checkpoint":               {Path: "gregale.yaml lifecycle.before_checkpoint"},
	"worker_replicas":                 {Path: "gregale workers scale"},
	"ports":                           {KnownGap: "no existing-app CLI setter; API-only today"},
	"favicon":                         {KnownGap: "no customer CLI setter; API-only today"},
	"robots_txt":                      {KnownGap: "no customer CLI setter; API-only today"},
	"head_wakes":                      {Path: "gregale app <slug> --head-wakes", AppFlags: []string{"head-wakes"}},
	"crawler_policy":                  {Path: "gregale app <slug> --crawler-policy", AppFlags: []string{"crawler-policy"}},
	"pre_auth_rate_limit":             {Path: "gregale app <slug> --pre-auth / --pre-auth-rps / --pre-auth-burst (route overrides stay API-only)", AppFlags: []string{"pre-auth", "pre-auth-rps", "pre-auth-burst"}},
	"health_path":                     {Path: "gregale app <slug> --health-path", AppFlags: []string{"health-path"}},
	"health_path_wakes":               {Path: "gregale app <slug> --health-path-wakes / --no-health-path-wakes", AppFlags: []string{"health-path-wakes", "no-health-path-wakes"}},
	"session_affinity":                {KnownGap: "no customer CLI setter; API-only today"},
	"version_affinity_cookie":         {KnownGap: "no customer CLI setter; API-only today"},
	"version_affinity_managed_cookie": {KnownGap: "no customer CLI setter; API-only today"},
	"revision_pin_ttl_seconds":        {KnownGap: "no customer CLI setter; API-only today"},
	"min_instances":                   {Path: "gregale app <slug> --min", AppFlags: []string{"min"}},
	"egress_allowlist":                {Path: "gregale app <slug> egress-allowlist {add|remove|clear}"},
	"egress_ports":                    {Path: "gregale app <slug> egress-ports {add|remove|clear}"},
	"autoscale_target_rps":            {Path: "gregale app <slug> --autoscale-target-rps", AppFlags: []string{"autoscale-target-rps"}},
	"autoscale_target_cpu_pct":        {Path: "gregale app <slug> --autoscale-target-cpu-pct", AppFlags: []string{"autoscale-target-cpu-pct"}},
	"streaming_enabled":               {Path: "gregale app <slug> --streaming-enabled / --no-streaming-enabled", AppFlags: []string{"streaming-enabled", "no-streaming-enabled"}},
	"websocket_enabled":               {Path: "gregale app <slug> --websocket-enabled / --no-websocket", AppFlags: []string{"websocket-enabled", "no-websocket"}},
	"app_protocol":                    {Path: "gregale app <slug> --app-protocol", AppFlags: []string{"app-protocol"}},
	"route_metrics_enabled":           {Path: "gregale app <slug> --route-metrics / --no-route-metrics", AppFlags: []string{"route-metrics", "no-route-metrics"}},
	"only_allow_declared_routes":      {Path: "gregale app <slug> --only-declared-routes / --no-only-declared-routes", AppFlags: []string{"only-declared-routes", "no-only-declared-routes"}},
	"declared_routes":                 {KnownGap: "no customer CLI setter for app route declarations; API-only today"},
	"maintenance_mode":                {Path: "gregale app <slug> --maintenance / --no-maintenance", AppFlags: []string{"maintenance", "no-maintenance"}},
	"require_signed":                  {Path: "gregale app <slug> security --require-signed=true|false"},
	"warm_snapshot_enabled":           {Path: "gregale app <slug> --warm-snapshot / --no-warm-snapshot", AppFlags: []string{"warm-snapshot", "no-warm-snapshot"}},
	"require_authn":                   {Path: "gregale app <slug> --require-authn / --no-require-authn", AppFlags: []string{"require-authn", "no-require-authn"}},
	"consumer_auth_mode":              {Path: "gregale app <slug> --consumer-auth-mode", AppFlags: []string{"consumer-auth-mode"}},
	"platform_tenant_required":        {Path: "gregale app <slug> --platform-tenant-required / --no-platform-tenant-required", AppFlags: []string{"platform-tenant-required", "no-platform-tenant-required"}},
	"public_auth":                     {Path: "gregale app <slug> --public-auth", AppFlags: []string{"public-auth"}},
	"warm_snapshot_min_requests":      {Path: "gregale app <slug> --warm-snapshot-min-requests", AppFlags: []string{"warm-snapshot-min-requests"}},
	"warm_snapshot_min_ms":            {Path: "gregale app <slug> --warm-snapshot-min-ms", AppFlags: []string{"warm-snapshot-min-ms"}},
	"warm_pool_size":                  {Path: "gregale app <slug> --warm-pool-size", AppFlags: []string{"warm-pool-size"}},
	"eviction_priority":               {Path: "gregale app <slug> --eviction-priority", AppFlags: []string{"eviction-priority"}},
	"overflow_node":                   {Path: "gregale app <slug> --overflow-node", AppFlags: []string{"overflow-node"}},
	"cors_default_enabled":            {KnownGap: "no customer CLI setter; API-only today"},
	"cors_default_origins":            {KnownGap: "no customer CLI setter; API-only today"},
	// The customer PATCH handler ignores this legacy internal field. It is
	// intentionally recorded as a gap so a DTO change forces an explicit review.
	"StartCommand":   {KnownGap: "internal reconciliation field; ignored by customer PATCH"},
	"scaling_policy": {Path: "gregale app <slug> --min / --autoscale-target-rps / --autoscale-target-cpu-pct", AppFlags: []string{"min", "autoscale-target-rps", "autoscale-target-cpu-pct"}},
}
