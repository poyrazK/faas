package fcvm

// adr: 624
import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type servingResumeTransport func(*http.Request) (*http.Response, error)

func (f servingResumeTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestJailerServingResumeOrdersNativeHookAndReadiness(t *testing.T) {
	for _, result := range []string{"fresh-restore", "existing-resident", "native-error", "hook-nack", "readiness-failure", "cancelled-probe"} {
		t.Run(result, func(t *testing.T) {
			lease := Lease{Instance: "r", HostIP: netip.MustParseAddr("127.0.0.1")}
			spec := ServingResumeSpec{AfterPausedRestore: result != "existing-resident", HealthcheckPath: "/regional-ready"}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var resumed, hooked atomic.Bool
			var probes atomic.Int32
			apiClient := &http.Client{Transport: servingResumeTransport(func(r *http.Request) (*http.Response, error) {
				var body struct {
					State string `json:"state"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil || r.Method != http.MethodPatch || r.URL.Path != "/vm" || body.State != "Resumed" {
					t.Error("unexpected native resume request", r.Method, r.URL, body, err)
				}
				resumed.Store(true)
				code := http.StatusNoContent
				if result == "native-error" {
					code = http.StatusBadRequest
				}
				return &http.Response{StatusCode: code, Status: http.StatusText(code), Body: io.NopCloser(strings.NewReader(""))}, nil
			})}
			v := &JailerVMM{chrootBase: shortChrootBase(t, "srv"), fcName: "f", readyTimeout: 30 * time.Millisecond, clients: map[string]*http.Client{lease.Instance: apiClient}}
			v.hcClient = &http.Client{Transport: servingResumeTransport(func(r *http.Request) (*http.Response, error) {
				probes.Add(1)
				if !resumed.Load() || (spec.AfterPausedRestore && !hooked.Load()) || r.URL.Path != spec.HealthcheckPath {
					t.Error("readiness preceded resume hook or lost retained path")
				}
				if result == "cancelled-probe" {
					cancel()
					return nil, context.Canceled
				}
				code := http.StatusOK
				if result == "readiness-failure" {
					code = http.StatusServiceUnavailable
				}
				return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(""))}, nil
			})}
			if spec.AfterPausedRestore && result != "native-error" {
				sock := v.vsockUDSSock(lease.Instance)
				if err := os.MkdirAll(filepath.Dir(sock), 0o700); err != nil {
					t.Fatal(err)
				}
				ack := byte(0)
				if result == "hook-nack" {
					ack = 1
				}
				fakeVsockUDSServer(t, sock, ack, func(nano int64, entropy []byte) error {
					if !resumed.Load() || nano <= 0 || len(entropy) != resumeHookEntropyBytes {
						return errors.New("hook preceded native resume or lacked fresh host input")
					}
					hooked.Store(true)
					return nil
				})
			}
			err := v.ResumeServingVM(ctx, lease, spec)
			if (err == nil) != (result == "fresh-restore" || result == "existing-resident") {
				t.Fatal("serving completion result", err)
			}
			if result == "cancelled-probe" && !errors.Is(err, context.Canceled) {
				t.Fatal("cancelled readiness acknowledged", err)
			}
			if (result == "native-error" || result == "hook-nack") && probes.Load() != 0 {
				t.Fatal("failed pre-readiness boundary ran a probe")
			}
			if err == nil && probes.Load() == 0 {
				t.Fatal("serving acknowledged without readiness")
			}
		})
	}
}
