package e2etest

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"
)

func TestRegistryHoldPreservesResponseAndCloseReleasesRequests(t *testing.T) {
	for _, releaseWithClose := range []bool{false, true} {
		t.Run(map[bool]string{false: "release", true: "close"}[releaseWithClose], func(t *testing.T) {
			registry := NewFakeRegistry()
			t.Cleanup(registry.Close)
			registry.AddImage("fixture", fakeImage{manifestDigest: "sha256:fixture", manifestBytes: []byte("reviewed-image")})
			entered, release := registry.HoldRepository("fixture")
			t.Cleanup(release)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() {
				request, err := http.NewRequestWithContext(ctx, http.MethodGet, registry.URL()+"/v2/fixture/manifests/latest", nil)
				if err != nil {
					done <- err
					return
				}
				response, err := http.DefaultClient.Do(request)
				if err == nil {
					defer response.Body.Close()
					var body []byte
					body, err = io.ReadAll(response.Body)
					if err == nil && (response.StatusCode != http.StatusOK || string(body) != "reviewed-image") {
						err = fmt.Errorf("released image response: status=%d body=%q", response.StatusCode, body)
					}
				}
				done <- err
			}()
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal("image pull never reached the held registry")
			}
			select {
			case err := <-done:
				t.Fatalf("held pull returned before release: %v", err)
			default:
			}
			if releaseWithClose {
				registry.Close()
			} else {
				release()
				release()
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		})
	}
}
