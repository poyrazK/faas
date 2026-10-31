package api

// Route awake patterns and suggestion kinds (ADR-1187).
const (
	RouteAwakePatternPeriodic  = "periodic"
	RouteAwakePatternIrregular = "irregular"

	// RouteAwakeSuggestEdgeCache: the route is anonymous, cacheable GET/HEAD
	// traffic; a cached response never wakes the app.
	RouteAwakeSuggestEdgeCache = "edge_cache"
	// RouteAwakeSuggestCallLessOften: a client calls the route more often
	// than the idle timeout, so the app never parks.
	RouteAwakeSuggestCallLessOften = "call_less_often"
	// RouteAwakeSuggestRunLessOften: a cron job fires more often than the
	// idle timeout, so the app never parks (ADR-20261022184757).
	RouteAwakeSuggestRunLessOften = "run_less_often"

	// Awake sources other than customer requests (ADR-1199).
	// RouteAwakeSourceRouteProbe: synthetic canary probes the app answered
	// (ADR-847). RouteAwakeSourcePrewarm: instances a scheduled prewarm
	// admitted.
	RouteAwakeSourceRouteProbe = "route_probe"
	RouteAwakeSourcePrewarm    = "prewarm"

	// Deliveries schedd made to the app (ADR-20261022184757), named after
	// the invocation sources.
	RouteAwakeSourceCron           = "cron"
	RouteAwakeSourceQueue          = "queue"
	RouteAwakeSourceDelayedTask    = "delayed_task"
	RouteAwakeSourceAsyncInvoke    = "async_invoke"
	RouteAwakeSourceInboundWebhook = "inbound_webhook"
	RouteAwakeSourceReplay         = "replay"
	RouteAwakeSourceEventSource    = "esm"
)

// RouteAwakeResponse is GET /v1/apps/{slug}/routes/awake: how long a
// scale-to-zero app was awake in the window and which routes kept it awake.
// Awake time is the billed running time (plan RAM + 8 MB per running second);
// routes are attributed from retained request telemetry at minute resolution.
type RouteAwakeResponse struct {
	Slug          string `json:"slug"`
	From          string `json:"from"`
	Until         string `json:"until"`
	WindowClamped bool   `json:"window_clamped,omitempty"`
	// IdleTimeoutSeconds is the app's effective idle timeout: how long it
	// stays awake after its last successful request.
	IdleTimeoutSeconds int `json:"idle_timeout_seconds"`
	// WaitUntilTimeoutSeconds is the plan's waitUntil task timeout when the
	// plan enables waitUntil. An instance parks no earlier than the later of
	// the idle timeout and its last waitUntil task, so a request keeps the
	// app awake for the longer of the two (ADR-20261022184757).
	WaitUntilTimeoutSeconds int     `json:"wait_until_timeout_seconds,omitempty"`
	WindowSeconds           int64   `json:"window_seconds"`
	AwakeSeconds            int64   `json:"awake_seconds"`
	AwakeShare              float64 `json:"awake_share"`
	MBSeconds               int64   `json:"mb_seconds"`
	GBHours                 float64 `json:"gb_hours"`
	Wakes                   int64   `json:"wakes"`
	// UnattributedSeconds is awake time no recorded request, probe,
	// prewarm or delivery explains: minimum instances, probes older than a
	// day, deliveries already pruned by result retention, or telemetry the
	// plan's rate cap dropped.
	UnattributedSeconds int64 `json:"unattributed_seconds"`
	// Sources is awake time credited to causes other than customer requests
	// (ADR-1199), for minutes no request explains.
	Sources []RouteAwakeSource `json:"sources"`
	// MinInstances is the app's current minimum instance count; above zero
	// the app never parks, so unattributed time is expected.
	MinInstances int `json:"min_instances"`
	// OtherRoutesSeconds is awake time attributed to routes outside the
	// busiest RoutesLimit.
	OtherRoutesSeconds int64             `json:"other_routes_seconds"`
	RoutesLimit        int               `json:"routes_limit"`
	Routes             []RouteAwakeRoute `json:"routes"`
}

// RouteAwakeSource is the awake time one non-request cause explains.
type RouteAwakeSource struct {
	// Source is route_probe, prewarm, or the invocation source of
	// deliveries schedd made to the app: cron, queue, delayed_task,
	// async_invoke, inbound_webhook, replay or esm.
	Source string `json:"source"`
	// Name identifies one cron job (the path or command it runs and its
	// schedule) or one queue; empty for other sources.
	Name string `json:"name,omitempty"`
	// Events are probe requests the app answered without a 5xx or an auth
	// rejection, instances a prewarm admitted, or deliveries that started
	// in the window.
	Events       int64   `json:"events"`
	AwakeSeconds int64   `json:"awake_seconds"`
	AwakeShare   float64 `json:"awake_share"`
	GBHours      float64 `json:"gb_hours"`
	// Pattern is periodic when a cron job fires at a steady interval;
	// IntervalSeconds is then the approximate interval.
	Pattern         string                `json:"pattern,omitempty"`
	IntervalSeconds int                   `json:"interval_seconds,omitempty"`
	Suggestion      *RouteAwakeSuggestion `json:"suggestion,omitempty"`
}

// RouteAwakeRoute is one route's share of the app's awake time.
type RouteAwakeRoute struct {
	Method string `json:"method"`
	Route  string `json:"route"`
	// Requests are successful requests that reached the app; only those
	// restart the idle timer.
	Requests     int64   `json:"requests"`
	Wakes        int64   `json:"wakes"`
	AwakeSeconds int64   `json:"awake_seconds"`
	AwakeShare   float64 `json:"awake_share"`
	GBHours      float64 `json:"gb_hours"`
	// Pattern is periodic when a client calls the route at a steady
	// interval; IntervalSeconds is then the approximate interval.
	Pattern         string                `json:"pattern"`
	IntervalSeconds int                   `json:"interval_seconds,omitempty"`
	IfRemoved       RouteAwakeWhatIf      `json:"if_removed"`
	Suggestion      *RouteAwakeSuggestion `json:"suggestion,omitempty"`
}

// RouteAwakeWhatIf estimates the app's awake time had this route's requests
// never reached it, with every other request unchanged.
type RouteAwakeWhatIf struct {
	AwakeSecondsSaved int64   `json:"awake_seconds_saved"`
	GBHoursSaved      float64 `json:"gb_hours_saved"`
	// WakesChange is the change in cold wakes: positive when other routes
	// would now find the app parked more often.
	WakesChange int64 `json:"wakes_change"`
}

// RouteAwakeSuggestion is a change that would let the app park.
type RouteAwakeSuggestion struct {
	Kind    string `json:"kind"`
	Message string `json:"message"`
}
