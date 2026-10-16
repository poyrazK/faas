// The native Start harness uses this loopback-only fault relay. It never
// changes a fleet's daemon versions, credentials, or gateway configuration.
package main

import (
	"bufio"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const capabilityPath = "/v1/deployments/response-check/capabilities"

var errCommitReplyLost = errors.New("fixture withheld commit reply")

type counters struct {
	Reads            uint64 `json:"reads"`
	Capabilities     uint64 `json:"capabilities"`
	CapabilityFaults uint64 `json:"capability_faults"`
	Checks           uint64 `json:"checks"`
	ResponseFaults   uint64 `json:"response_faults"`
	Writes           uint64 `json:"writes"`
	AppCreates       uint64 `json:"app_creates"`
	SecretWrites     uint64 `json:"secret_writes"`
	SourceWrites     uint64 `json:"source_writes"`
	OtherWrites      uint64 `json:"other_writes"`
	BlockedWrites    uint64 `json:"blocked_writes"`
	ForwardedWrites  uint64 `json:"forwarded_writes"`
	CommitFaults     uint64 `json:"commit_faults"`
	UploadReads      uint64 `json:"upload_reads"`
}

type commitReceipt struct {
	UploadID     string `json:"upload_id"`
	DeploymentID string `json:"deployment_id"`
	AppID        string `json:"app_id"`
}

type snapshot struct {
	Mode        string         `json:"mode"`
	BlockWrites bool           `json:"block_writes"`
	Counts      counters       `json:"counts"`
	Commit      *commitReceipt `json:"commit,omitempty"`
	CommitError bool           `json:"commit_error"`
}

type relay struct {
	mu        sync.Mutex
	state     snapshot
	auth      string
	proxy     *httputil.ReverseProxy
	transport *http.Transport
	changed   chan struct{}
}

func newRelay(origin, token string) (*relay, error) {
	target, err := url.Parse(origin)
	if err != nil || target.Hostname() == "" || target.User != nil || target.Path != "" ||
		target.RawQuery != "" || target.Fragment != "" || token == "" {
		return nil, errors.New("invalid relay target or missing fixture credential")
	}
	ip := net.ParseIP(target.Hostname())
	localHTTP := target.Scheme == "http" && (target.Hostname() == "localhost" || ip.IsLoopback())
	if target.Scheme != "https" && !localHTTP {
		return nil, errors.New("relay upstream requires HTTPS or local test loopback")
	}
	// Clone the normal transport: preserve HTTP(S)_PROXY, NO_PROXY, trusted
	// system roots and SSL_CERT_FILE. Never disable TLS verification.
	transport := http.DefaultTransport.(*http.Transport).Clone()
	proxy := &httputil.ReverseProxy{
		Rewrite: func(request *httputil.ProxyRequest) {
			request.SetURL(target)
			request.Out.Header.Del("Proxy-Authorization")
		},
		Transport: transport, FlushInterval: -1,
		ErrorLog: log.New(io.Discard, "", 0),
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			if errors.Is(err, errCommitReplyLost) {
				// Close the stream before any acceptance headers or body reach
				// the CLI. The server has already committed normally.
				panic(http.ErrAbortHandler)
			}
			reply(w, http.StatusBadGateway, map[string]string{"error": "fixture upstream unavailable"})
		},
	}
	handler := &relay{auth: "Bearer " + token, proxy: proxy, transport: transport,
		state: snapshot{Mode: "healthy", BlockWrites: true}, changed: make(chan struct{})}
	proxy.ModifyResponse = handler.commitReply
	return handler, nil
}

func validID(value string) bool {
	parsed, err := uuid.Parse(value)
	return err == nil && parsed != uuid.Nil && parsed.String() == value
}

func validUploadID(value string) bool {
	_, err := hex.DecodeString(value)
	return err == nil && len(value) == 24 && value == strings.ToLower(value)
}

func uploadCommit(request *http.Request) string {
	parts := strings.Split(request.URL.Path, "/")
	if request.Method == http.MethodPost && len(parts) == 5 && parts[1] == "v1" &&
		parts[2] == "uploads" && validUploadID(parts[3]) && parts[4] == "commit" {
		return parts[3]
	}
	return ""
}

func (r *relay) commitReply(response *http.Response) error {
	upload := uploadCommit(response.Request)
	r.mu.Lock()
	mode, changed := r.state.Mode, r.changed
	r.mu.Unlock()
	if upload == "" || mode != "lost-commit-reply" && mode != "hold-commit-reply" {
		return nil
	}
	// Consume only the bounded acceptance response. Retain protocol IDs, never
	// the deployment source, response body, request headers, or credentials.
	wire, err := io.ReadAll(io.LimitReader(response.Body, (64<<10)+1))
	_ = response.Body.Close()
	var accepted struct {
		ID    string `json:"id"`
		AppID string `json:"app_id"`
	}
	r.mu.Lock()
	r.state.Counts.CommitFaults++
	if response.StatusCode == http.StatusCreated {
		if err != nil || len(wire) > 64<<10 || json.Unmarshal(wire, &accepted) != nil || !validID(accepted.ID) || !validID(accepted.AppID) {
			r.state.CommitError = true
		} else {
			receipt := commitReceipt{UploadID: upload, DeploymentID: accepted.ID, AppID: accepted.AppID}
			if r.state.Commit != nil && *r.state.Commit != receipt {
				r.state.CommitError = true
			} else {
				r.state.Commit = &receipt
			}
		}
	}
	r.mu.Unlock()
	if mode == "hold-commit-reply" {
		select {
		case <-changed:
		case <-response.Request.Context().Done():
		}
	}
	// Suppress retries too, including already-committed error responses that
	// might otherwise reveal acceptance to the still-running CLI.
	return errCommitReplyLost
}

func reply(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func responseCheck(request *http.Request) bool {
	parts := strings.Split(strings.TrimPrefix(request.URL.Path, "/"), "/")
	return request.Method == http.MethodPost && len(parts) == 6 && parts[0] == "v1" && parts[1] == "apps" &&
		parts[2] != "" && parts[3] == "deployments" && len(parts[4]) == 36 && parts[5] == "response-check"
}

func (r *relay) ServeHTTP(w http.ResponseWriter, request *http.Request) {
	if request.URL.IsAbs() || request.Method == http.MethodConnect ||
		subtle.ConstantTimeCompare([]byte(request.Header.Get("Authorization")), []byte(r.auth)) != 1 {
		reply(w, http.StatusForbidden, map[string]string{"error": "outside acceptance fixture"})
		return
	}
	check := responseCheck(request)
	write := request.Method != http.MethodGet && request.Method != http.MethodHead && !check
	r.mu.Lock()
	// This relay belongs to an already-owned workload. Even its healthy
	// baseline must never create an unpinned app.
	mode, blocked := r.state.Mode, write && (r.state.BlockWrites || request.URL.Path == "/v1/apps")
	r.record(request, write, check, blocked)
	r.mu.Unlock()
	if blocked {
		reply(w, http.StatusServiceUnavailable, map[string]string{"error": "acceptance mutation guard"})
		return
	}
	if request.Method == http.MethodGet && request.URL.Path == capabilityPath {
		switch mode {
		case "old-api":
			reply(w, http.StatusNotFound, map[string]string{"error": "unsupported fixture endpoint"})
			return
		case "old-gateway", "outage":
			reason := "gateway_unsupported"
			if mode == "outage" {
				reason = "gateway_unavailable"
			}
			reply(w, http.StatusOK, map[string]any{"protocol_version": 1, "available": false, "unavailable_reason": reason})
			return
		}
	}
	if check && mode == "response-outage" {
		reply(w, http.StatusServiceUnavailable, map[string]string{"error": "fixture verification unavailable"})
		return
	}
	// ReverseProxy streams bodies and events without buffering or persisting
	// uploads, credentials, responses, or terminal output.
	r.proxy.ServeHTTP(w, request)
}

// Caller holds mu. Only fixed categories and counts leave the child process.
func (r *relay) record(request *http.Request, write, check, blocked bool) {
	count := &r.state.Counts
	if write {
		count.Writes++
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/v1/apps":
			count.AppCreates++
		case strings.Contains(request.URL.Path, "/secrets"):
			count.SecretWrites++
		case strings.Contains(request.URL.Path, "/uploads") || strings.Contains(request.URL.Path, "/deployments"):
			count.SourceWrites++
		default:
			count.OtherWrites++
		}
		if blocked {
			count.BlockedWrites++
		} else {
			count.ForwardedWrites++
		}
	} else if check {
		count.Checks++
		if r.state.Mode == "response-outage" {
			count.ResponseFaults++
		}
	} else {
		count.Reads++
		parts := strings.Split(request.URL.Path, "/")
		if request.Method == http.MethodGet && len(parts) == 4 && parts[1] == "v1" && parts[2] == "uploads" && validUploadID(parts[3]) {
			count.UploadReads++
		}
		if request.Method == http.MethodGet && request.URL.Path == capabilityPath {
			count.Capabilities++
			if r.state.Mode == "old-api" || r.state.Mode == "old-gateway" || r.state.Mode == "outage" {
				count.CapabilityFaults++
			}
		}
	}
}

func (r *relay) command(line []byte) (snapshot, error) {
	var command struct {
		Mode        string `json:"mode"`
		BlockWrites *bool  `json:"block_writes"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(line)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&command) != nil {
		return snapshot{}, errors.New("invalid relay command")
	}
	var extra any
	if !errors.Is(decoder.Decode(&extra), io.EOF) {
		return snapshot{}, errors.New("invalid trailing relay command")
	}
	switch command.Mode {
	case "", "healthy", "old-api", "old-gateway", "outage", "response-outage", "lost-commit-reply", "hold-commit-reply":
	default:
		return snapshot{}, errors.New("invalid relay mode")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if command.Mode != "" {
		if command.Mode != r.state.Mode {
			close(r.changed)
			r.changed = make(chan struct{})
			if command.Mode == "lost-commit-reply" || command.Mode == "hold-commit-reply" {
				r.state.Commit, r.state.CommitError = nil, false
			}
		}
		r.state.Mode = command.Mode
	}
	if command.BlockWrites != nil {
		r.state.BlockWrites = *command.BlockWrites
	}
	return r.state, nil
}

func run(origin, token string, input io.Reader, output io.Writer) error {
	handler, err := newRelay(origin, token)
	if err != nil {
		return err
	}
	defer handler.transport.CloseIdleConnections()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return errors.New("could not bind acceptance loopback")
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, MaxHeaderBytes: 16 << 10,
		ErrorLog: log.New(io.Discard, "", 0)}
	defer func() { _ = server.Close() }()
	go func() { _ = server.Serve(listener) }()
	encoder := json.NewEncoder(output)
	if encoder.Encode(map[string]any{"version": 1, "origin": "http://" + listener.Addr().String()}) != nil {
		return errors.New("could not publish acceptance listener")
	}
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 4096), 4096)
	for scanner.Scan() {
		state, err := handler.command(scanner.Bytes())
		if err != nil {
			return err
		}
		if encoder.Encode(state) != nil {
			return errors.New("could not publish acceptance counters")
		}
	}
	if scanner.Err() != nil {
		return errors.New("could not read acceptance control")
	}
	return nil
}

func main() {
	if run(strings.TrimRight(os.Getenv("FAAS_API"), "/"), os.Getenv("FAAS_TOKEN"), os.Stdin, os.Stdout) != nil {
		_, _ = io.WriteString(os.Stderr, "Start acceptance relay failed.\n")
		os.Exit(1)
	}
}
