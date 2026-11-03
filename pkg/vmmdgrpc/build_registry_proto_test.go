package vmmdgrpc

import (
	"context"
	"reflect"
	"testing"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/fcvm"
)

// adr: 20261026091500 — a builder's sealed registry credentials cross the
// vmmd adapter unopened.
func TestToColdBootRequestBuildRegistryCredentials(t *testing.T) {
	request, err := toColdBootRequest(context.Background(), &vmmdpb.CreateColdBootRequest{
		Instance: "build-1",
		App:      &vmmdpb.AppSpec{BaseKey: "/base", LayerKey: "/layer"},
		Build: &vmmdpb.BuildSpec{ExportDir: "/export", RegistryCredentials: []*vmmdpb.BuildRegistryCredential{
			{Registry: "ghcr.io", Username: "octo", PasswordSealed: []byte("sealed")},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []fcvm.BuildRegistryCredential{{Registry: "ghcr.io", Username: "octo", PasswordSealed: []byte("sealed")}}
	if !reflect.DeepEqual(request.BuildRegistryCredentials, want) || request.ExportDir != "/export" {
		t.Fatalf("registry credentials = %+v, export dir = %q", request.BuildRegistryCredentials, request.ExportDir)
	}
	app, err := toColdBootRequest(context.Background(), &vmmdpb.CreateColdBootRequest{Instance: "i-app", App: &vmmdpb.AppSpec{BaseKey: "/base", LayerKey: "/layer"}})
	if err != nil || app.BuildRegistryCredentials != nil {
		t.Fatalf("app VM = %+v, %v", app.BuildRegistryCredentials, err)
	}
}
