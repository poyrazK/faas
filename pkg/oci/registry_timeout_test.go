package oci

import (
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func defaultPullTimeout() time.Duration {
	return time.Duration(api.OCIPullTimeoutSeconds) * time.Second
}

// TestRegistryClientTimeout_NeverUnbounded is the regression pin.
//
// NewEgressHTTPClient deliberately returns a client with no Timeout — every
// caller is expected to supply its own. WithHTTPClient used to replace the
// constructor's default client wholesale, including its 60s deadline, so
// NewRegistryClient(WithHTTPClient(NewEgressHTTPClient())) produced a client
// that would wait on a stalled registry forever. cmd/gregale/commands_doctor.go
// constructs exactly that, which made `gregale doctor` hang indefinitely
// against a slow or hostile registry.
func TestRegistryClientTimeout_NeverUnbounded(t *testing.T) {
	tests := []struct {
		name string
		opts []Option
		want time.Duration
	}{
		{
			name: "no options uses the default",
			opts: nil,
			want: defaultPullTimeout(),
		},
		{
			name: "egress client with no explicit timeout falls back to the default",
			opts: []Option{WithHTTPClient(NewEgressHTTPClient())},
			want: defaultPullTimeout(),
		},
		{
			name: "explicit timeout after WithHTTPClient",
			opts: []Option{WithHTTPClient(NewEgressHTTPClient()), WithTimeout(7 * time.Second)},
			want: 7 * time.Second,
		},
		{
			// The order that silently lost the deadline before.
			name: "explicit timeout BEFORE WithHTTPClient",
			opts: []Option{WithTimeout(7 * time.Second), WithHTTPClient(NewEgressHTTPClient())},
			want: 7 * time.Second,
		},
		{
			name: "timeout alone",
			opts: []Option{WithTimeout(3 * time.Second)},
			want: 3 * time.Second,
		},
		{
			name: "caller client's own timeout is honoured",
			opts: []Option{WithHTTPClient(&http.Client{Timeout: 11 * time.Second})},
			want: 11 * time.Second,
		},
		{
			name: "explicit timeout overrides the caller client's own",
			opts: []Option{WithHTTPClient(&http.Client{Timeout: 11 * time.Second}), WithTimeout(2 * time.Second)},
			want: 2 * time.Second,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := NewRegistryClient(tc.opts...)
			if c.hc.Timeout == 0 {
				t.Fatalf("registry client has no deadline — it will wait on a stalled registry forever")
			}
			if c.hc.Timeout != tc.want {
				t.Fatalf("timeout = %v, want %v", c.hc.Timeout, tc.want)
			}
		})
	}
}

// TestRegistryClientTimeout_DoesNotMutateCallerClient pins that a shared
// *http.Client handed to two RegistryClients is not retuned by the second.
// WithTimeout used to write straight through to c.hc.Timeout, so the last
// constructor to run silently changed the deadline of every earlier client
// built from the same value.
func TestRegistryClientTimeout_DoesNotMutateCallerClient(t *testing.T) {
	shared := &http.Client{Timeout: 30 * time.Second}

	first := NewRegistryClient(WithHTTPClient(shared), WithTimeout(5*time.Second))
	second := NewRegistryClient(WithHTTPClient(shared), WithTimeout(45*time.Second))

	if shared.Timeout != 30*time.Second {
		t.Fatalf("caller's client was mutated: Timeout = %v, want 30s", shared.Timeout)
	}
	if first.hc.Timeout != 5*time.Second {
		t.Fatalf("first client Timeout = %v, want 5s", first.hc.Timeout)
	}
	if second.hc.Timeout != 45*time.Second {
		t.Fatalf("second client Timeout = %v, want 45s", second.hc.Timeout)
	}
	if first.hc == second.hc {
		t.Fatal("both clients share one *http.Client; their deadlines cannot differ")
	}
}
