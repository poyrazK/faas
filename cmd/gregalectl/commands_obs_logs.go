// commands_obs_logs.go — `gregalectl obs logs` (ADR-20261023124955 §2).
//
// Searches the platform logs every host ships to Loki on the control plane,
// so an operator can follow one wake, request or trace across hosts without
// SSH and journalctl on each node or a Grafana tunnel. The command reads Loki
// directly over its mTLS listener; it never goes through apid, because Loki
// is not a customer-intent store and needs no admin session.
//
// Connection settings resolve per key: flag, then environment
// ($FAAS_LOKI_URL, $FAAS_LOKI_SERVER_NAME, $FAAS_LOKI_TENANT,
// $FAAS_LOKI_CA_FILE, $FAAS_LOKI_CERT_FILE, $FAAS_LOKI_KEY_FILE), then
// /etc/faas/loki-client.env, which the loki role renders on the control plane
// when faas_obs_logs_enabled is set. The client certificate defaults to the
// Prometheus client pair, so run the command as root on the control plane.
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const subObsLogs = "logs"

// lokiClientEnvFile is rendered by the loki role (ADR-20261023124955).
var lokiClientEnvFile = "/etc/faas/loki-client.env"

const (
	obsLogsDefaultLimit = 200
	// Loki's max_entries_limit_per_query (roles/loki defaults).
	obsLogsMaxLimit = 5000
	// Loki keeps 14 days (loki_retention_period).
	obsLogsMaxRange = 14 * 24 * time.Hour
)

var (
	obsLogsIDRE    = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)
	obsLogsLabelRE = regexp.MustCompile(`^[A-Za-z0-9@._:-]{1,128}$`)
)

type obsLogsOptions struct {
	query                           string
	host, unit, level, app, account string
	wakeID, requestID, traceID      string
	grep                            string
	start, end                      time.Time
	limit                           int
	forward                         bool
}

type lokiClientConfig struct {
	url, serverName, tenant   string
	caFile, certFile, keyFile string
}

type obsLogEntry struct {
	TS     time.Time         `json:"ts"`
	Labels map[string]string `json:"labels"`
	Line   string            `json:"line"`
}

func cmdObsLogs(args []string) int {
	fs := flag.NewFlagSet(subObsLogs, flag.ContinueOnError)
	fs.SetOutput(osStderr)
	opts, cfg, jsonOut, timeout := obsLogsFlags(fs)
	since := fs.Duration("since", time.Hour, "how far back from --to to search (maximum 14d)")
	from := fs.String("from", "", "RFC3339 start (overrides --since)")
	to := fs.String("to", "", "RFC3339 end (default now)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if err := opts.finish(fs.NArg(), *since, *from, *to, time.Now()); err != nil {
		_, _ = fmt.Fprintf(osStderr, "gregalectl obs logs: %v\n", err)
		return 2
	}
	if err := cfg.resolve(); err != nil {
		_, _ = fmt.Fprintf(osStderr, "gregalectl obs logs: %v\n", err)
		return 2
	}
	query := opts.logQL()
	entries, err := queryLoki(context.Background(), cfg, *timeout, query, opts)
	if err != nil {
		_, _ = fmt.Fprintf(osStderr, "gregalectl obs logs: %v\n", err)
		return 1
	}
	if *jsonOut || jsonOutput || jsonFlagEnabled(args) {
		return emitOperatorJSON(struct {
			Query   string        `json:"query"`
			Start   time.Time     `json:"start"`
			End     time.Time     `json:"end"`
			Entries []obsLogEntry `json:"entries"`
		}{query, opts.start.UTC(), opts.end.UTC(), entries})
	}
	writeObsLogs(osStdout, entries, opts.limit)
	return 0
}

func obsLogsFlags(fs *flag.FlagSet) (*obsLogsOptions, *lokiClientConfig, *bool, *time.Duration) {
	o, c := &obsLogsOptions{}, &lokiClientConfig{}
	fs.StringVar(&o.wakeID, "wake-id", "", "lines that mention this wake_id")
	fs.StringVar(&o.requestID, "request-id", "", "lines that mention this request_id")
	fs.StringVar(&o.traceID, "trace-id", "", "lines that mention this trace_id")
	fs.StringVar(&o.host, "host", "", "only this host (the host label, e.g. fsn-3)")
	fs.StringVar(&o.unit, "unit", "", "only this unit: schedd, faas-schedd.service or another systemd unit")
	fs.StringVar(&o.level, "level", "", "only this level: debug, info, warn or error")
	fs.StringVar(&o.app, "app", "", "only customer app-log records of this app_id")
	fs.StringVar(&o.account, "account", "", "only customer app-log records of this account_id")
	fs.StringVar(&o.grep, "grep", "", "only lines containing this text")
	fs.StringVar(&o.query, "query", "", "raw LogQL; replaces every filter flag")
	fs.IntVar(&o.limit, "limit", obsLogsDefaultLimit, "maximum lines (1..5000)")
	fs.BoolVar(&o.forward, "forward", false, "return the oldest lines in the range instead of the newest")
	fs.StringVar(&c.url, "loki-url", "", "Loki base URL (default: $FAAS_LOKI_URL or "+lokiClientEnvFile+")")
	fs.StringVar(&c.serverName, "loki-server-name", "", "name in the Loki server certificate")
	fs.StringVar(&c.tenant, "loki-tenant", "", "Loki tenant (default gregale)")
	fs.StringVar(&c.caFile, "loki-ca", "", "CA file that signed the Loki server certificate")
	fs.StringVar(&c.certFile, "loki-cert", "", "client certificate file")
	fs.StringVar(&c.keyFile, "loki-key", "", "client key file")
	jsonOut := fs.Bool("json", false, "emit structured JSON")
	timeout := fs.Duration("timeout", 30*time.Second, "HTTP timeout")
	return o, c, jsonOut, timeout
}

// finish validates the flags and fixes the time range.
func (o *obsLogsOptions) finish(nargs int, since time.Duration, from, to string, now time.Time) error {
	if nargs != 0 {
		return errors.New("no positional arguments; filter with flags")
	}
	if o.limit < 1 || o.limit > obsLogsMaxLimit {
		return fmt.Errorf("--limit must be 1..%d", obsLogsMaxLimit)
	}
	for name, v := range map[string]string{"--wake-id": o.wakeID, "--request-id": o.requestID, "--trace-id": o.traceID} {
		if v != "" && !obsLogsIDRE.MatchString(v) {
			return fmt.Errorf("%s must be letters, digits and ._:- only", name)
		}
	}
	for name, v := range map[string]string{"--host": o.host, "--unit": o.unit, "--app": o.app, "--account": o.account} {
		if v != "" && !obsLogsLabelRE.MatchString(v) {
			return fmt.Errorf("%s must be letters, digits and @._:- only", name)
		}
	}
	if o.level != "" {
		o.level = strings.ToUpper(o.level)
		if o.level != "DEBUG" && o.level != "INFO" && o.level != "WARN" && o.level != "ERROR" {
			return errors.New("--level must be debug, info, warn or error")
		}
	}
	o.end = now
	if to != "" {
		t, err := time.Parse(time.RFC3339Nano, to)
		if err != nil {
			return errors.New("--to must be RFC3339")
		}
		o.end = t
	}
	o.start = o.end.Add(-since)
	if from != "" {
		t, err := time.Parse(time.RFC3339Nano, from)
		if err != nil {
			return errors.New("--from must be RFC3339")
		}
		o.start = t
	}
	if since <= 0 || !o.start.Before(o.end) || o.end.Sub(o.start) > obsLogsMaxRange {
		return errors.New("the range must be positive and at most 14d (Loki's retention)")
	}
	return nil
}

// logQL builds the query from the filter flags. Labels select streams;
// ids and --grep are line filters, because request_id, wake_id and trace_id
// stay in the line and never become labels (ADR-143).
func (o *obsLogsOptions) logQL() string {
	if o.query != "" {
		return o.query
	}
	matchers := []string{`job="faas-journal"`}
	for _, l := range []struct{ name, value string }{
		{"host", o.host}, {"unit", obsLogsUnit(o.unit)}, {"level", o.level},
		{"app_id", o.app}, {"account_id", o.account},
	} {
		if l.value != "" {
			matchers = append(matchers, l.name+"="+strconv.Quote(l.value))
		}
	}
	q := "{" + strings.Join(matchers, ",") + "}"
	for _, needle := range []string{o.wakeID, o.requestID, o.traceID, o.grep} {
		if needle != "" {
			q += " |= " + strconv.Quote(needle)
		}
	}
	return q
}

// obsLogsUnit accepts "schedd" for faas-schedd.service.
func obsLogsUnit(u string) string {
	if u == "" || strings.Contains(u, ".") {
		return u
	}
	if !strings.HasPrefix(u, "faas-") {
		u = "faas-" + u
	}
	return u + ".service"
}

func (c *lokiClientConfig) resolve() error {
	file := readLokiClientEnv(lokiClientEnvFile)
	pick := func(flagValue, env, fallback string) string {
		if flagValue != "" {
			return flagValue
		}
		if v := os.Getenv(env); v != "" {
			return v
		}
		if v := file[env]; v != "" {
			return v
		}
		return fallback
	}
	c.url = strings.TrimRight(pick(c.url, "FAAS_LOKI_URL", ""), "/")
	c.serverName = pick(c.serverName, "FAAS_LOKI_SERVER_NAME", "")
	c.tenant = pick(c.tenant, "FAAS_LOKI_TENANT", "gregale")
	c.caFile = pick(c.caFile, "FAAS_LOKI_CA_FILE", "")
	c.certFile = pick(c.certFile, "FAAS_LOKI_CERT_FILE", "")
	c.keyFile = pick(c.keyFile, "FAAS_LOKI_KEY_FILE", "")
	if c.url == "" {
		return fmt.Errorf("no Loki URL: platform logs are off on this host (faas_obs_logs_enabled), or pass --loki-url; see docs/ops/logs.md")
	}
	u, err := url.Parse(c.url)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return fmt.Errorf("--loki-url %q is not an http(s) URL", c.url)
	}
	if u.Scheme == "https" && (c.caFile == "" || c.certFile == "" || c.keyFile == "") {
		return errors.New("an https Loki URL needs --loki-ca, --loki-cert and --loki-key (Loki requires client certificates)")
	}
	return nil
}

// readLokiClientEnv parses KEY=VALUE lines; a missing file is not an error.
func readLokiClientEnv(path string) map[string]string {
	out := map[string]string{}
	raw, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok {
			out[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	return out
}

func lokiHTTPClient(c *lokiClientConfig, timeout time.Duration) (*http.Client, error) {
	client := &http.Client{Timeout: timeout}
	if !strings.HasPrefix(c.url, "https://") {
		return client, nil
	}
	caPEM, err := os.ReadFile(c.caFile)
	if err != nil {
		return nil, fmt.Errorf("read Loki CA: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("read Loki CA %s: no PEM certificates", c.caFile)
	}
	pair, err := tls.LoadX509KeyPair(c.certFile, c.keyFile)
	if err != nil {
		return nil, fmt.Errorf("load Loki client certificate: %w", err)
	}
	client.Transport = &http.Transport{TLSClientConfig: &tls.Config{
		MinVersion:   tls.VersionTLS12,
		RootCAs:      pool,
		Certificates: []tls.Certificate{pair},
		ServerName:   c.serverName,
	}}
	return client, nil
}

func queryLoki(ctx context.Context, c *lokiClientConfig, timeout time.Duration, query string, o *obsLogsOptions) ([]obsLogEntry, error) {
	client, err := lokiHTTPClient(c, timeout)
	if err != nil {
		return nil, err
	}
	direction := "backward"
	if o.forward {
		direction = "forward"
	}
	params := url.Values{
		"query":     {query},
		"start":     {strconv.FormatInt(o.start.UnixNano(), 10)},
		"end":       {strconv.FormatInt(o.end.UnixNano(), 10)},
		"limit":     {strconv.Itoa(o.limit)},
		"direction": {direction},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url+"/loki/api/v1/query_range?"+params.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("build Loki request: %w", err)
	}
	req.Header.Set("X-Scope-OrgID", c.tenant)
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("query Loki at %s: %w", c.url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, fmt.Errorf("read Loki response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		// Loki answers a bad query with a plain-text parse error; show it.
		snippet := strings.TrimSpace(string(body))
		if len(snippet) > 300 {
			snippet = snippet[:300] + "…"
		}
		return nil, fmt.Errorf("query rejected with HTTP %d: %s", resp.StatusCode, snippet)
	}
	return parseLokiStreams(body)
}

func parseLokiStreams(body []byte) ([]obsLogEntry, error) {
	var out struct {
		Status string `json:"status"`
		Data   struct {
			ResultType string `json:"resultType"`
			Result     []struct {
				Stream map[string]string `json:"stream"`
				Values [][2]string       `json:"values"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("decode Loki response: %w", err)
	}
	if out.Status != "success" || out.Data.ResultType != "streams" {
		return nil, fmt.Errorf("unexpected Loki response (status %q, type %q); use a log query, not a metric query", out.Status, out.Data.ResultType)
	}
	entries := []obsLogEntry{}
	for _, s := range out.Data.Result {
		for _, v := range s.Values {
			ns, err := strconv.ParseInt(v[0], 10, 64)
			if err != nil {
				return nil, fmt.Errorf("decode Loki timestamp %q: %w", v[0], err)
			}
			entries = append(entries, obsLogEntry{TS: time.Unix(0, ns).UTC(), Labels: s.Stream, Line: v[1]})
		}
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].TS.Before(entries[j].TS) })
	return entries, nil
}

// writeObsLogs prints one line per entry, oldest first:
// time host unit LEVEL message key=value...
func writeObsLogs(w io.Writer, entries []obsLogEntry, limit int) {
	for _, e := range entries {
		unit := strings.TrimSuffix(strings.TrimPrefix(e.Labels["unit"], "faas-"), ".service")
		_, _ = fmt.Fprintf(w, "%s %s %s %s\n", e.TS.Format("2006-01-02T15:04:05.000Z"), orDash(e.Labels["host"]), orDash(unit), formatObsLogLine(e.Line))
	}
	if len(entries) == limit {
		_, _ = fmt.Fprintf(osStderr, "gregalectl obs logs: showing the %d-line limit; narrow the range or raise --limit\n", limit)
	}
	if len(entries) == 0 {
		_, _ = fmt.Fprintln(osStderr, "gregalectl obs logs: no matching lines in the range")
	}
}

// formatObsLogLine renders a slog JSON line as LEVEL msg key=value...;
// anything else is printed as it is.
func formatObsLogLine(line string) string {
	var fields map[string]any
	if err := json.Unmarshal([]byte(line), &fields); err != nil {
		return line
	}
	level, _ := fields["level"].(string)
	msg, _ := fields["msg"].(string)
	keys := make([]string, 0, len(fields))
	for k := range fields {
		if k != "level" && k != "msg" && k != "time" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString(orDash(level))
	b.WriteString(" ")
	b.WriteString(msg)
	for _, k := range keys {
		b.WriteString(" ")
		b.WriteString(k)
		b.WriteString("=")
		switch v := fields[k].(type) {
		case string:
			if strings.ContainsAny(v, " \t\"=") || v == "" {
				b.WriteString(strconv.Quote(v))
			} else {
				b.WriteString(v)
			}
		default:
			raw, _ := json.Marshal(v)
			b.Write(raw)
		}
	}
	return b.String()
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
