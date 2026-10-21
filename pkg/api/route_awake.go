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
	IdleTimeoutSeconds int     `json:"idle_timeout_seconds"`
	WindowSeconds      int64   `json:"window_seconds"`
	AwakeSeconds       int64   `json:"awake_seconds"`
	AwakeShare         float64 `json:"awake_share"`
	MBSeconds          int64   `json:"mb_seconds"`
	GBHours            float64 `json:"gb_hours"`
	Wakes              int64   `json:"wakes"`
	// UnattributedSeconds is awake time no recorded request explains:
	// synthetic probes, scheduled prewarm, minimum instances, queue, cron and
	// waitUntil work, or telemetry the plan's rate cap dropped.
	UnattributedSeconds int64 `json:"unattributed_seconds"`
	// OtherRoutesSeconds is awake time attributed to routes outside the
	// busiest RoutesLimit.
	OtherRoutesSeconds int64             `json:"other_routes_seconds"`
	RoutesLimit        int               `json:"routes_limit"`
	Routes             []RouteAwakeRoute `json:"routes"`
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
