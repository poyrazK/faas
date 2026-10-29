// adr: 20261023115741 — faas.Client exposes GA and beta methods only;
// preview.Client adds the preview ones.
package preview_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	faas "github.com/poyrazK/faas/sdk/go"
	"github.com/poyrazK/faas/sdk/go/preview"
)

func TestPreviewMethodsLiveOnlyOnThePreviewClient(t *testing.T) {
	public := reflect.TypeOf(&faas.Client{})
	full := reflect.TypeOf(&preview.Client{})
	for _, name := range []string{"ListApps", "ListCrons", "GetCapabilities"} {
		if _, ok := public.MethodByName(name); !ok {
			t.Errorf("faas.Client lost the GA method %s", name)
		}
		if _, ok := full.MethodByName(name); !ok {
			t.Errorf("preview.Client must keep the GA method %s", name)
		}
	}
	for _, name := range []string{"QueueSend", "CreateExecution", "InvokeDurableEntity", "CreateNativeIssueTicket"} {
		if _, ok := public.MethodByName(name); ok {
			t.Errorf("faas.Client exposes the preview method %s", name)
		}
		if _, ok := full.MethodByName(name); !ok {
			t.Errorf("preview.Client lacks the preview method %s", name)
		}
	}
}

func TestPreviewClientSharesTheTransport(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path+" "+r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"registry_version":1,"plan":"pro","capabilities":[]}`))
	}))
	defer server.Close()
	c, err := preview.NewClient(server.URL, "tok")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetCapabilities(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(paths) != 1 || paths[0] != "/v1/capabilities Bearer tok" {
		t.Fatalf("requests = %v", paths)
	}
}
