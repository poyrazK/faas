package commit

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
)

func TestHTTPAcceptorRefusesCredentialRedirect(t *testing.T) {
	var destinationCalls atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		destinationCalls.Add(1)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer destination.Close()
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
	}))
	defer endpoint.Close()
	for _, custom := range []bool{false, true} {
		t.Run(map[bool]string{false: "default", true: "injected"}[custom], func(t *testing.T) {
			a := HTTPAcceptor{URL: endpoint.URL, Token: "source-private-token"}
			client := &http.Client{}
			if custom {
				a.Client = client
			}
			_, err := a.Accept(context.Background(), Event{ID: uuid.NewString(), Type: "order.created", Data: []byte(`{}`)})
			if err == nil || destinationCalls.Load() != 0 {
				t.Fatalf("redirect followed: calls=%d err=%v", destinationCalls.Load(), err)
			}
			if client.CheckRedirect != nil || client.Timeout != 0 {
				t.Fatal("injected client mutated")
			}
		})
	}
}
