package main

import (
	"errors"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// queuesAudience is workloadidentity.QueuesAudience, the audience a pull
// consumer presents to apid's lease API (ADR-1140). The fixture keeps its
// imports minimal, so the value is repeated here.
const queuesAudience = "gregale:queues"

// serveIdentityProbe adds /identity for the -identity-probe fixture: it asks
// guest-init's loopback workload identity endpoint for a token and relays
// the status and body unchanged, so a test can verify the token.
func serveIdentityProbe(mux *http.ServeMux) {
	mux.HandleFunc("/identity", func(w http.ResponseWriter, r *http.Request) {
		endpoint, err := workloadIdentityURL(r.URL.Query().Get("audience"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		client := &http.Client{Timeout: 10 * time.Second}
		resp, err := client.Get(endpoint)
		if err != nil {
			http.Error(w, "identity endpoint unreachable: "+err.Error(), http.StatusBadGateway)
			return
		}
		defer func() { _ = resp.Body.Close() }()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, io.LimitReader(resp.Body, 16<<10))
	})
}

// runQueueConsumer is the -queue-consumer fixture: a pull-queue worker with
// no listener. Like the SDK consumers before their first lease, it asks
// guest-init for a gregale:queues token as soon as it starts, retrying until
// one is minted, and then idles. A guest in the metal harness cannot reach
// apid, so the test leases on the worker's behalf; vmmd's mint counter marks
// the moment this worker could have sent its first lease.
func runQueueConsumer() {
	endpoint, err := workloadIdentityURL(queuesAudience)
	if err != nil {
		log.Fatalf("queue-consumer: %v", err)
	}
	start := time.Now()
	client := &http.Client{Timeout: 5 * time.Second}
	for attempt := 1; ; attempt++ {
		resp, err := client.Get(endpoint)
		if err == nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 16<<10))
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				log.Printf("queue-consumer: %s token after %s (%d attempts)", queuesAudience, time.Since(start), attempt)
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	// Idle until stopped. A bare select{} dies with "all goroutines are
	// asleep" once the client's keep-alive connection closes, and the
	// restart would mint another token.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)
	<-stop
}

func workloadIdentityURL(audience string) (string, error) {
	endpoint, err := url.Parse(os.Getenv("FAAS_WORKLOAD_IDENTITY_ENDPOINT"))
	if err != nil || endpoint.Host == "" {
		return "", errors.New("no workload identity endpoint")
	}
	query := endpoint.Query()
	query.Set("audience", audience)
	endpoint.RawQuery = query.Encode()
	return endpoint.String(), nil
}
