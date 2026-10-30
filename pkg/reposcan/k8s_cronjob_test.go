package reposcan

import (
	"slices"
	"strings"
	"testing"
)

func k8sCronJob(spec string) string {
	return `apiVersion: batch/v1
kind: CronJob
metadata: {name: report}
spec:
  schedule: "0 7 * * *"
` + spec + `  jobTemplate:
    spec:
      template:
        spec:
          containers: [{name: report, image: acme/report:1}]
`
}

func TestScanWithOptions_KubernetesCronJobSpec(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, spec   string
		opts         ScanOptions
		wantZone     string
		wantWarnings []string
		noWarnings   []string
	}{
		{name: "time zone runs the schedule", spec: "  timeZone: Europe/Berlin\n", opts: k8sOn(),
			wantZone: "Europe/Berlin", noWarnings: []string{"timeZone"}},
		{name: "time zone reported without the capability", spec: "  timeZone: Europe/Berlin\n", opts: ScanOptions{},
			wantWarnings: []string{"report: timeZone Europe/Berlin ignored: the schedule runs in UTC"}},
		{name: "no time zone is UTC", opts: k8sOn(), noWarnings: []string{"timeZone"}},
		{name: "forbid", spec: "  concurrencyPolicy: Forbid\n", opts: k8sOn(),
			wantWarnings: []string{"concurrencyPolicy Forbid not applied: runs may overlap", "--skip-if-running"}},
		{name: "replace", spec: "  concurrencyPolicy: Replace\n", opts: k8sOn(),
			wantWarnings: []string{"concurrencyPolicy Replace not applied", "overlap replace"}},
		{name: "allow matches", spec: "  concurrencyPolicy: Allow\n", opts: k8sOn(), noWarnings: []string{"concurrencyPolicy"}},
		{name: "starting deadline", spec: "  startingDeadlineSeconds: 300\n", opts: k8sOn(),
			wantWarnings: []string{"startingDeadlineSeconds 300 not applied", "start_deadline_seconds"}},
		{name: "history limits", spec: "  successfulJobsHistoryLimit: 3\n  failedJobsHistoryLimit: 1\n", opts: k8sOn(),
			wantWarnings: []string{"successfulJobsHistoryLimit and failedJobsHistoryLimit ignored", "gregale cron runs"}},
		{name: "fields not reported without the capability", spec: "  concurrencyPolicy: Forbid\n  startingDeadlineSeconds: 300\n  failedJobsHistoryLimit: 1\n",
			opts: ScanOptions{}, noWarnings: []string{"concurrencyPolicy", "startingDeadlineSeconds", "HistoryLimit"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result, err := ScanWithOptions(k8sPodFS(k8sCronJob(tc.spec)), tc.opts)
			if err != nil {
				t.Fatal(err)
			}
			i := slices.IndexFunc(result.Workloads, func(w Workload) bool { return w.Name == "report" })
			if i < 0 {
				t.Fatalf("workloads = %+v, want report", result.Workloads)
			}
			schedules := result.Workloads[i].CronSchedules()
			if len(schedules) != 1 || schedules[0].Expression != "0 7 * * *" || schedules[0].Timezone != tc.wantZone {
				t.Errorf("schedules = %+v, want 0 7 * * * in %q", schedules, tc.wantZone)
			}
			joined := strings.Join(result.Warnings, "\n")
			for _, want := range tc.wantWarnings {
				if !strings.Contains(joined, want) {
					t.Errorf("warnings missing %q:\n%s", want, joined)
				}
			}
			for _, unwanted := range tc.noWarnings {
				if strings.Contains(joined, unwanted) {
					t.Errorf("warnings mention %q:\n%s", unwanted, joined)
				}
			}
		})
	}
}

func TestScanWithOptions_KubernetesCronJobBadTimeZone(t *testing.T) {
	t.Parallel()
	for _, zone := range []string{"Mars/Olympus", "Local"} {
		_, err := ScanWithOptions(k8sPodFS(k8sCronJob("  timeZone: "+zone+"\n")), k8sOn())
		if err == nil || !strings.Contains(err.Error(), `timeZone "`+zone+`" is not an IANA time zone`) {
			t.Errorf("zone %s: err = %v, want an IANA time zone error", zone, err)
		}
	}
}

func TestScanWithOptions_KubernetesDeploymentWithoutService(t *testing.T) {
	t.Parallel()
	const worker = `apiVersion: apps/v1
kind: Deployment
metadata: {name: worker}
spec:
  template:
    metadata: {labels: {app: worker}}
    spec:
      containers: [{name: worker, image: acme/worker:1}]
`
	const service = `---
apiVersion: v1
kind: Service
metadata: {name: worker}
spec: {selector: {app: worker}, ports: [{port: 80}]}
`
	const otherNamespace = `---
apiVersion: v1
kind: Service
metadata: {name: worker, namespace: elsewhere}
spec: {selector: {app: worker}, ports: [{port: 80}]}
`
	const want = "worker: no Service selects this Deployment and it declares no containerPort, so it runs in request mode; set `execution_mode: worker`"
	cases := []struct {
		name, manifests string
		opts            ScanOptions
		warn            bool
	}{
		{"no Service and no port", worker, k8sOn(), true},
		{"a Service selects it", worker + service, k8sOn(), false},
		{"a Service in another namespace", worker + otherNamespace, k8sOn(), true},
		{"a containerPort", strings.Replace(worker, "image: acme/worker:1}", "image: acme/worker:1, ports: [{containerPort: 8080}]}", 1), k8sOn(), false},
		{"without the capability", worker, ScanOptions{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result, err := ScanWithOptions(k8sPodFS(tc.manifests), tc.opts)
			if err != nil {
				t.Fatal(err)
			}
			joined := strings.Join(result.Warnings, "\n")
			if got := strings.Contains(joined, want); got != tc.warn {
				t.Errorf("warned = %v, want %v:\n%s", got, tc.warn, joined)
			}
		})
	}
}
