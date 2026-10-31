package reposcan

import (
	"reflect"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func intPtr(v int) *int { return &v }

func TestComposeResources(t *testing.T) {
	for _, tc := range []struct {
		name, service string
		env           string
		want          *api.ComposeResources
		warning       string
	}{
		{"deploy limits and replicas", "deploy:\n      replicas: 3\n      resources:\n        limits: {memory: 512M, cpus: '0.5'}",
			"", &api.ComposeResources{Replicas: intPtr(3), MemoryMB: 512, CPUMillicores: 500}, ""},
		{"legacy keys", "scale: 2\n    mem_limit: 1g\n    cpus: 1",
			"", &api.ComposeResources{Replicas: intPtr(2), MemoryMB: 1024, CPUMillicores: 1000}, ""},
		{"deploy wins over legacy keys", "mem_limit: 1g\n    deploy:\n      resources:\n        limits: {memory: 256m}",
			"", &api.ComposeResources{MemoryMB: 256}, ""},
		{"bytes round up to MiB", "mem_limit: 1048577", "", &api.ComposeResources{MemoryMB: 2}, ""},
		{"binary suffixes", "deploy:\n      resources:\n        limits: {memory: 1.5GiB}", "", &api.ComposeResources{MemoryMB: 1536}, ""},
		{"cpus round up to a share", "cpus: 0.3", "", &api.ComposeResources{CPUMillicores: 500}, "cpus 0.3 rounded up to 0.5"},
		{"cpus above one", "deploy:\n      resources:\n        limits: {cpus: '2'}", "", &api.ComposeResources{CPUMillicores: 1000},
			"deploy.resources.limits.cpus 2 lowered to 1"},
		{"interpolated", "deploy:\n      replicas: ${REPLICAS}\n      resources:\n        limits: {memory: \"${MEMORY}\"}",
			"REPLICAS=4\nMEMORY=768m\n", &api.ComposeResources{Replicas: intPtr(4), MemoryMB: 768}, ""},
		{"zero replicas are ignored", "deploy: {replicas: 0}", "", nil, "deploy.replicas 0 ignored"},
		{"unsupported deploy keys warn", "deploy:\n      replicas: 2\n      restart_policy: {condition: on-failure}\n      resources:\n        reservations: {memory: 128m}\n        limits: {pids: 100}",
			"", &api.ComposeResources{Replicas: intPtr(2)}, "deploy.restart_policy ignored: the platform starts, parks and restarts services itself"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := "services:\n  web:\n    image: nginx:1.27\n    ports: [80]\n    " + tc.service + "\n"
			var extra []string
			if tc.env != "" {
				extra = []string{".env", tc.env}
			}
			result, err := scanCompose(t, body, extra...)
			if err != nil {
				t.Fatal(err)
			}
			if got := composeBuildWorkload(t, result, "web").Resources; !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("resources = %+v, want %+v", got, tc.want)
			}
			if tc.warning != "" && !containsWarning(result.Warnings, tc.warning) {
				t.Fatalf("warnings %q lack %q", result.Warnings, tc.warning)
			}
			for _, key := range []string{"deploy ignored", "scale ignored", "mem_limit ignored", "cpus ignored"} {
				if containsWarning(result.Warnings, ": "+key) {
					t.Fatalf("applied key still warned: %q", result.Warnings)
				}
			}
		})
	}
	t.Run("reservations and limits keys warn", func(t *testing.T) {
		body := "services:\n  web:\n    image: nginx:1.27\n    deploy:\n      resources:\n        reservations: {memory: 128m}\n        limits: {pids: 100}\n"
		result, err := scanCompose(t, body)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"deploy.resources.reservations ignored", "deploy.resources.limits.pids ignored"} {
			if !containsWarning(result.Warnings, want) {
				t.Fatalf("warnings %q lack %q", result.Warnings, want)
			}
		}
	})
	for _, tc := range []struct {
		name, service, want string
	}{
		{"negative replicas", "deploy: {replicas: -1}", "deploy.replicas: -1 must be a whole number"},
		{"bad memory", "mem_limit: lots", `mem_limit: "lots" must be a size`},
		{"zero memory", "mem_limit: 0", "mem_limit: 0 must be"},
		{"bad cpus", "cpus: many", `cpus: "many" must be a number of CPUs`},
		{"negative cpus", "cpus: -1", "must be a positive number of CPUs"},
		{"deploy not a mapping", "deploy: 3", "deploy must be a mapping"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := "services:\n  web:\n    image: nginx:1.27\n    " + tc.service + "\n"
			if _, err := scanCompose(t, body); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
	t.Run("plans show them", func(t *testing.T) {
		result, err := scanCompose(t, "services:\n  web:\n    build: .\n    ports: [3000]\n    mem_limit: 512m\n", "Dockerfile", "FROM node:22\n")
		if err != nil {
			t.Fatal(err)
		}
		want := &api.ComposeResources{MemoryMB: 512}
		if got := PlanWorkload(composeBuildWorkload(t, result, "web")).Resources; !reflect.DeepEqual(got, want) {
			t.Fatalf("plan resources = %+v, want %+v", got, want)
		}
	})
}
