package main

import (
	"bytes"
	"context"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/statusmirror"
)

type offlineMemStore struct {
	objects map[string]statusmirror.Object
	n       int
}

func (m *offlineMemStore) Get(_ context.Context, key string) (statusmirror.Object, error) {
	object, ok := m.objects[key]
	if !ok {
		return statusmirror.Object{}, statusmirror.ErrNotFound
	}
	return object, nil
}

func (m *offlineMemStore) Put(_ context.Context, key string, body []byte, _, ifMatch string) error {
	current, exists := m.objects[key]
	if (ifMatch == "-" && exists) || (ifMatch != "" && ifMatch != "-" && current.ETag != ifMatch) {
		return statusmirror.ErrConflict
	}
	m.n++
	m.objects[key] = statusmirror.Object{Body: append([]byte(nil), body...), ETag: strconv.Itoa(m.n)}
	return nil
}

func TestStatusOfflinePostResolveAndList(t *testing.T) {
	store := &offlineMemStore{objects: map[string]statusmirror.Object{}}
	oldStore, oldNow, oldOut := statusOfflineStore, statusOfflineNow, osStdout
	statusOfflineStore = func() (statusmirror.Store, error) { return store, nil }
	statusOfflineNow = func() time.Time { return time.Date(2026, 11, 15, 9, 0, 0, 0, time.UTC) }
	var out bytes.Buffer
	osStdout = &out
	t.Cleanup(func() { statusOfflineStore, statusOfflineNow, osStdout = oldStore, oldNow, oldOut })

	code := cmdStatusDispatch([]string{"offline", "post", "--title", "API unreachable", "--impact", "major_outage", "--components", "api_console,networking", "--message", "Investigating."})
	if code != 0 {
		t.Fatalf("post exit = %d", code)
	}
	id := regexp.MustCompile(`id: ([0-9a-f-]{36})`).FindStringSubmatch(out.String())
	if id == nil {
		t.Fatalf("post output has no id: %q", out.String())
	}
	out.Reset()
	if code := cmdStatusDispatch([]string{"offline", "resolve", "--id", id[1], "--message", "Recovered."}); code != 0 {
		t.Fatalf("resolve exit = %d", code)
	}
	out.Reset()
	if code := cmdStatusDispatch([]string{"offline", "list"}); code != 0 {
		t.Fatalf("list exit = %d", code)
	}
	if !strings.Contains(out.String(), id[1]) || !strings.Contains(out.String(), "resolved") {
		t.Fatalf("list output = %q", out.String())
	}
}

func TestStatusOfflineRejectsInvalidImpactWithoutWriting(t *testing.T) {
	store := &offlineMemStore{objects: map[string]statusmirror.Object{}}
	oldStore, oldErr := statusOfflineStore, osStderr
	statusOfflineStore = func() (statusmirror.Store, error) { return store, nil }
	osStderr = &bytes.Buffer{}
	t.Cleanup(func() { statusOfflineStore, osStderr = oldStore, oldErr })
	if code := cmdStatusDispatch([]string{"offline", "post", "--title", "x", "--impact", "operational", "--components", "api_console", "--message", "m"}); code == 0 {
		t.Fatal("invalid impact must fail")
	}
	if len(store.objects) != 0 {
		t.Fatal("rejected notice must not write")
	}
}
