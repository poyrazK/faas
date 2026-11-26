package sandboxes_test

// adr: 20261027090013 step 14 — preset code-interpreter boots the
// platform's code-interpreter image as an image session; it is
// sandbox_unsupported until the operator names the image, and takes no
// image, template or pull of its own.

import (
	"context"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestCreateCodeInterpreter(t *testing.T) {
	f := newFixture(t)
	var ensured []api.SandboxImageSource
	f.h.EnsureImage = func(_ context.Context, _ state.Account, src api.SandboxImageSource) (string, string, *api.Problem) {
		ensured = append(ensured, src)
		return "11111111-1111-1111-1111-111111111111", src.Image, nil
	}
	var p api.Problem
	if code := f.call(t, f.h.Create, pro(), http.MethodPost, "/v1/sandboxes", `{"preset":"code-interpreter"}`, "", &p); code != http.StatusUnprocessableEntity ||
		p.Code != api.CodeSandboxUnsupported {
		t.Fatalf("without the image = %d %+v", code, p)
	}

	const image = "ghcr.io/onebox-faas/sandbox-code-interpreter@sha256:" + "ab0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcd"
	f.h.CodeInterpreterImage = image
	sb, code := f.create(t, pro(), `{"preset":"code-interpreter"}`)
	if code != http.StatusCreated || sb.Preset != api.SandboxPresetImage || sb.Image != image {
		t.Fatalf("create = %d %+v", code, sb)
	}
	if len(ensured) != 1 || ensured[0].Image != image || ensured[0].Pull != "" || ensured[0].RegistryAuth != nil {
		t.Fatalf("ensured %+v", ensured)
	}
	for _, body := range []string{
		`{"preset":"code-interpreter","image":"ghcr.io/acme/box"}`,
		`{"preset":"code-interpreter","template":"mine"}`,
		`{"preset":"code-interpreter","pull":"always"}`,
	} {
		if code := f.call(t, f.h.Create, pro(), http.MethodPost, "/v1/sandboxes", body, "", &p); code != http.StatusBadRequest {
			t.Fatalf("%s = %d %+v", body, code, p)
		}
	}
	if len(ensured) != 1 {
		t.Fatalf("a refused request cached an image: %+v", ensured)
	}
}
