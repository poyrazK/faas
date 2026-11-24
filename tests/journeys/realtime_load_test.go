package journeys

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 20261123074615 — core realtime joins beta with a load run against the
// deployed product as its evidence. The run is opt-in even in the full tier:
// it holds GREGALE_E2E_REALTIME_LOAD connections (default 500, under the
// edge's per-address limit) on one endpoint for a few minutes.
//
// It connects the clients on the original protocol, holds them idle, then
// measures two fan-outs from the backend: a principal send that reaches every
// connection of one principal across the realtime nodes, and a channel
// publish to connections the backend subscribed. Every client must stay
// connected and receive every message; delivery latency is reported.
//
// Set GREGALE_E2E_REALTIME_LOAD_OUT to a file path to write the measurements
// as JSON (the evidence committed under docs/ops/evidence/).
func TestRealtimeLoad(t *testing.T) {
	total := realtimeLoadSize(t)
	tg := needs(t, tierFull, true)
	j := &realtimeJourney{owner: t, tg: tg, slug: tg.app(t, "rtload")}
	j.key = tg.scopedKey(t, "rtload")
	t.Cleanup(func() { j.cleanup(t) })
	load := &realtimeLoad{j: j, total: total, channelSize: min(total/4, 250), messages: 10}
	t.Cleanup(load.close)
	s := newStory(t)
	s.step("realtime_enabled", j.enabled)
	s.step("deploy_issuer_app", j.deployIssuerApp)
	s.step("create_endpoint", j.createEndpoint)
	s.step("connect_all", load.connectAll)
	s.step("hold_idle", load.holdIdle)
	s.step("principal_fan_out", load.principalFanOut)
	s.step("subscribe_channel_group", load.subscribeChannelGroup)
	s.step("channel_fan_out", load.channelFanOut)
	s.check("all_still_connected", load.allConnected)
	s.check("write_evidence", load.writeEvidence)
	s.step("delete_endpoint", load.deleteEndpoint)
}

const (
	realtimeLoadFanPrincipal     = "load-fan"
	realtimeLoadChannelPrincipal = "load-ch"
	realtimeLoadChannel          = "load-room"
	realtimeLoadDialers          = 20
	realtimeLoadHold             = 90 * time.Second
	realtimeLoadDeliveryWait     = 30 * time.Second
	realtimeLoadP99Budget        = 2 * time.Second
)

func realtimeLoadSize(t *testing.T) int {
	raw := os.Getenv("GREGALE_E2E_REALTIME_LOAD")
	if raw == "" {
		t.Skip("opt-in: set GREGALE_E2E_REALTIME_LOAD=1 (or a connection count) to run the realtime load journey")
	}
	if raw == "1" {
		return realtimeLoadDefault
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 8 || n > realtimeLoadMax {
		t.Fatalf("GREGALE_E2E_REALTIME_LOAD=%q: want 1 or a connection count from 8 to %d", raw, realtimeLoadMax)
	}
	return n
}

// One machine is one client address, and the edge holds at most
// EdgeClientOpenConnsPerAddrMax open connections per address (ADR
// 20261103163954); past it the edge resets new connections. The run stays
// under it, so it measures realtime, not the per-address guard.
var (
	realtimeLoadMax     = api.EdgeClientOpenConnsPerAddrMax - 12
	realtimeLoadDefault = realtimeLoadMax
)

type realtimeLoad struct {
	j           *realtimeJourney
	total       int
	channelSize int
	messages    int

	mu      sync.Mutex
	clients []*loadClient
	result  realtimeLoadResult
}

type realtimeLoadResult struct {
	Connections       int               `json:"connections"`
	ChannelGroup      int               `json:"channel_group"`
	HandshakeMS       latencySummary    `json:"handshake_ms"`
	HandshakeRetries  int64             `json:"handshake_retries"`
	HoldSeconds       float64           `json:"hold_seconds"`
	DroppedDuringHold int               `json:"dropped_during_hold"`
	PrincipalFanOut   fanOutMeasure     `json:"principal_fan_out"`
	ChannelFanOut     fanOutMeasure     `json:"channel_fan_out"`
	ConnectedAtEnd    int               `json:"connected_at_end"`
	Listed            int               `json:"listed_in_inventory"`
	NodesQueried      int               `json:"realtime_nodes_queried"`
	API               string            `json:"api"`
	StartedAt         time.Time         `json:"started_at"`
	Notes             map[string]string `json:"notes,omitempty"`
}

type fanOutMeasure struct {
	Messages   int            `json:"messages"`
	Recipients int            `json:"recipients"`
	Expected   int            `json:"expected_deliveries"`
	Delivered  int            `json:"delivered"`
	Missing    int            `json:"missing"`
	LatencyMS  latencySummary `json:"delivery_latency_ms"`
}

type latencySummary struct {
	Count int     `json:"count"`
	P50   float64 `json:"p50"`
	P95   float64 `json:"p95"`
	P99   float64 `json:"p99"`
	Max   float64 `json:"max"`
}

func summarize(samples []time.Duration) latencySummary {
	if len(samples) == 0 {
		return latencySummary{}
	}
	sorted := slices.Clone(samples)
	slices.Sort(sorted)
	at := func(q float64) float64 {
		i := int(q * float64(len(sorted)-1))
		return float64(sorted[i].Microseconds()) / 1000
	}
	return latencySummary{Count: len(sorted), P50: at(0.50), P95: at(0.95), P99: at(0.99), Max: at(1)}
}

// loadClient records when each tagged message arrives.
type loadClient struct {
	conn      *websocket.Conn
	principal string
	mu        sync.Mutex
	arrived   map[string]time.Time
	closed    error
}

func (c *loadClient) read() {
	for {
		_, data, err := c.conn.ReadMessage()
		now := time.Now()
		c.mu.Lock()
		if err != nil {
			c.closed = err
			c.mu.Unlock()
			return
		}
		// Messages are "rtload <tag>"; anything else is ignored.
		if tag, ok := strings.CutPrefix(string(data), "rtload "); ok {
			c.arrived[tag] = now
		}
		c.mu.Unlock()
	}
}

func (c *loadClient) alive() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed == nil
}

func (c *loadClient) arrival(tag string) (time.Time, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	at, ok := c.arrived[tag]
	return at, ok
}

func (l *realtimeLoad) close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, c := range l.clients {
		_ = c.conn.Close()
	}
}

// connectAll opens every connection with a bounded number of dialers. The
// channel group gets its own principal so the backend can find its
// connections by principal.
func (l *realtimeLoad) connectAll(t *testing.T) {
	l.result.StartedAt, l.result.API = time.Now().UTC(), l.j.tg.api
	tokens := map[string]string{
		realtimeLoadFanPrincipal:     l.j.sign(t, realtimeLoadFanPrincipal),
		realtimeLoadChannelPrincipal: l.j.sign(t, realtimeLoadChannelPrincipal),
	}
	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		latencies []time.Duration
		failures  []string
		retries   atomic.Int64
		next      atomic.Int64
	)
	dialer := websocket.Dialer{HandshakeTimeout: 20 * time.Second}
	for range realtimeLoadDialers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				i := int(next.Add(1)) - 1
				if i >= l.total {
					return
				}
				principal := realtimeLoadFanPrincipal
				if i < l.channelSize {
					principal = realtimeLoadChannelPrincipal
				}
				header := http.Header{"Authorization": {"Bearer " + tokens[principal]}}
				var conn *websocket.Conn
				var lastErr string
				for attempt := 0; attempt < 6 && conn == nil; attempt++ {
					if attempt > 0 {
						retries.Add(1)
						time.Sleep(time.Duration(attempt) * time.Second)
					}
					start := time.Now()
					c, code, status, err := wsDial(&dialer, l.j.wsURL(), header)
					if err != nil {
						lastErr = fmt.Sprintf("%v (%d %s)", err, code, status)
						continue
					}
					mu.Lock()
					latencies = append(latencies, time.Since(start))
					mu.Unlock()
					conn = c
				}
				if conn == nil {
					mu.Lock()
					failures = append(failures, lastErr)
					mu.Unlock()
					continue
				}
				client := &loadClient{conn: conn, principal: principal, arrived: map[string]time.Time{}}
				go client.read()
				l.mu.Lock()
				l.clients = append(l.clients, client)
				l.mu.Unlock()
			}
		}()
	}
	wg.Wait()
	l.result.Connections, l.result.ChannelGroup = len(l.clients), l.channelSize
	l.result.HandshakeMS, l.result.HandshakeRetries = summarize(latencies), retries.Load()
	t.Logf("connected %d/%d in %s; handshake p50 %.0f ms p95 %.0f ms p99 %.0f ms; %d retries",
		len(l.clients), l.total, time.Since(l.result.StartedAt).Round(time.Second),
		l.result.HandshakeMS.P50, l.result.HandshakeMS.P95, l.result.HandshakeMS.P99, retries.Load())
	if len(failures) > 0 {
		t.Fatalf("%d of %d connections failed; first: %s", len(failures), l.total, failures[0])
	}
}

// holdIdle keeps every connection open with no traffic: realtimed answers
// heartbeats without calling the app.
func (l *realtimeLoad) holdIdle(t *testing.T) {
	start := time.Now()
	time.Sleep(realtimeLoadHold)
	l.result.HoldSeconds = time.Since(start).Seconds()
	dropped := 0
	for _, c := range l.snapshot() {
		if !c.alive() {
			dropped++
		}
	}
	l.result.DroppedDuringHold = dropped
	if dropped > 0 {
		t.Fatalf("%d of %d connections dropped while idle for %s", dropped, len(l.snapshot()), realtimeLoadHold)
	}
	l.inventory(t)
}

// inventory checks that the fleet-wide connection inventory lists every open
// connection, and records how many realtime nodes answered.
func (l *realtimeLoad) inventory(t *testing.T) {
	listed, cursor := 0, ""
	for page := 0; page < 10; page++ {
		path := l.j.endpointPath() + "/connections?limit=1000"
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		r := l.j.tg.apiCall(http.MethodGet, path, nil, l.j.key)
		if r.status != http.StatusOK {
			t.Fatalf("connection inventory: %s", r)
		}
		doc := jsonBody(t, r)
		list, _ := field(doc, "connections").([]any)
		listed += len(list)
		l.result.NodesQueried = int(num(doc, "nodes_queried"))
		if str(doc, "partial") == "true" {
			t.Errorf("connection inventory is partial: a realtime node did not answer")
		}
		if cursor = str(doc, "next_cursor"); cursor == "" {
			break
		}
	}
	l.result.Listed = listed
	if listed != l.total {
		t.Errorf("connection inventory lists %d connections, want %d", listed, l.total)
	}
}

// deleteEndpoint deletes the endpoint and checks every load connection is
// closed by it.
func (l *realtimeLoad) deleteEndpoint(t *testing.T) {
	l.j.tg.ok(t, time.Minute, "realtime", "delete", l.j.slug, l.j.endpoint, "--yes")
	l.j.deleted = true
	eventually(t, "every load connection closed after delete", time.Minute, 2*time.Second, func() (bool, string) {
		open := 0
		for _, c := range l.snapshot() {
			if c.alive() {
				open++
			}
		}
		return open == 0, fmt.Sprintf("%d still open", open)
	})
}

func (l *realtimeLoad) snapshot() []*loadClient {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.clients)
}

func (l *realtimeLoad) group(principal string) []*loadClient {
	return slices.DeleteFunc(l.snapshot(), func(c *loadClient) bool { return c.principal != principal })
}

// principalFanOut sends live messages to every connection of one principal.
func (l *realtimeLoad) principalFanOut(t *testing.T) {
	recipients := l.group(realtimeLoadFanPrincipal)
	l.result.PrincipalFanOut = l.fanOut(t, "principal", recipients, func(tag string) httpResult {
		return l.j.tg.apiCall(http.MethodPost, l.j.endpointPath()+"/principals:send", map[string]any{
			"principal":   realtimeLoadFanPrincipal,
			"data_base64": base64.StdEncoding.EncodeToString([]byte("rtload " + tag)),
		}, l.j.key)
	})
}

// subscribeChannelGroup subscribes the channel group's connections the way a
// backend does on the original protocol: find them by principal, then
// subscribe each.
func (l *realtimeLoad) subscribeChannelGroup(t *testing.T) {
	r := l.j.tg.apiCall(http.MethodGet, l.j.endpointPath()+"/connections?limit=1000&principal="+realtimeLoadChannelPrincipal, nil, l.j.key)
	if r.status != http.StatusOK {
		t.Fatalf("list the channel group's connections: %s", r)
	}
	list, _ := field(jsonBody(t, r), "connections").([]any)
	if len(list) != l.channelSize {
		t.Fatalf("inventory lists %d %s connections, want %d", len(list), realtimeLoadChannelPrincipal, l.channelSize)
	}
	var (
		wg       sync.WaitGroup
		failures atomic.Int64
		first    atomic.Value
		ids      = make(chan string)
	)
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for id := range ids {
				var res httpResult
				for attempt := 0; attempt < 5; attempt++ {
					res = l.j.tg.apiCall(http.MethodPut, l.j.endpointPath()+"/connections/"+url.PathEscape(id)+"/subscriptions/"+realtimeLoadChannel, nil, l.j.key)
					if res.status != http.StatusTooManyRequests && res.status < 500 {
						break
					}
					time.Sleep(time.Duration(attempt+1) * time.Second)
				}
				if res.status/100 != 2 {
					failures.Add(1)
					first.CompareAndSwap(nil, res.String())
				}
			}
		}()
	}
	for _, c := range list {
		ids <- str(c, "id")
	}
	close(ids)
	wg.Wait()
	if n := failures.Load(); n > 0 {
		t.Fatalf("%d of %d subscriptions failed; first: %v", n, len(list), first.Load())
	}
}

// channelFanOut publishes live messages to the subscribed channel group.
func (l *realtimeLoad) channelFanOut(t *testing.T) {
	recipients := l.group(realtimeLoadChannelPrincipal)
	l.result.ChannelFanOut = l.fanOut(t, "channel", recipients, func(tag string) httpResult {
		return l.j.tg.apiCall(http.MethodPost, l.j.endpointPath()+"/channels/"+realtimeLoadChannel+"/publish", map[string]any{
			"data_base64": base64.StdEncoding.EncodeToString([]byte("rtload " + tag)),
		}, l.j.key)
	})
}

// fanOut sends l.messages tagged messages one second apart and measures,
// per recipient, the time from the API call to arrival.
func (l *realtimeLoad) fanOut(t *testing.T, kind string, recipients []*loadClient, send func(tag string) httpResult) fanOutMeasure {
	m := fanOutMeasure{Messages: l.messages, Recipients: len(recipients), Expected: l.messages * len(recipients)}
	sent := map[string]time.Time{}
	for i := range l.messages {
		tag := fmt.Sprintf("%s-%d-%s", kind, i, randomHex(t, 3))
		start := time.Now()
		if r := send(tag); r.status/100 != 2 {
			t.Fatalf("%s send %d: %s", kind, i, r)
		}
		sent[tag] = start
		time.Sleep(time.Second)
	}
	deadline := time.Now().Add(realtimeLoadDeliveryWait)
	var samples []time.Duration
	for {
		samples, m.Delivered = samples[:0], 0
		for tag, start := range sent {
			for _, c := range recipients {
				if at, ok := c.arrival(tag); ok {
					m.Delivered++
					samples = append(samples, at.Sub(start))
				}
			}
		}
		if m.Delivered == m.Expected || time.Now().After(deadline) {
			break
		}
		time.Sleep(time.Second)
	}
	m.Missing, m.LatencyMS = m.Expected-m.Delivered, summarize(samples)
	t.Logf("%s fan-out: %d/%d delivered to %d connections; p50 %.0f ms p95 %.0f ms p99 %.0f ms max %.0f ms",
		kind, m.Delivered, m.Expected, len(recipients), m.LatencyMS.P50, m.LatencyMS.P95, m.LatencyMS.P99, m.LatencyMS.Max)
	if m.Missing > 0 {
		t.Errorf("%s fan-out lost %d of %d deliveries", kind, m.Missing, m.Expected)
	}
	if p99 := time.Duration(m.LatencyMS.P99 * float64(time.Millisecond)); p99 > realtimeLoadP99Budget {
		t.Errorf("%s fan-out p99 %s exceeds the %s budget", kind, p99, realtimeLoadP99Budget)
	}
	return m
}

func (l *realtimeLoad) allConnected(t *testing.T) {
	alive := 0
	for _, c := range l.snapshot() {
		if c.alive() {
			alive++
		}
	}
	l.result.ConnectedAtEnd = alive
	if alive != l.total {
		t.Errorf("%d of %d connections open at the end", alive, l.total)
	}
}

func (l *realtimeLoad) writeEvidence(t *testing.T) {
	out, err := json.MarshalIndent(l.result, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("realtime load result:\n%s", out)
	if path := os.Getenv("GREGALE_E2E_REALTIME_LOAD_OUT"); path != "" {
		if err := os.WriteFile(path, append(out, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
