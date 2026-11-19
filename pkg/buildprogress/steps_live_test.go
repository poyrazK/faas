package buildprogress_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/buildprogress"
)

// A Dockerfile build's console as the builder VM mirrors it: guest-init and
// buildkitd lines interleaved with buildctl's plain progress.
const dockerfileConsole = `guest-init: build-command
time="2026-11-18T10:00:00Z" level=info msg="running server"
#0 building with "default" instance using oci worker
#1 [internal] load build definition from Dockerfile
#1 transferring dockerfile: 512B done
#1 DONE 0.1s
#2 [internal] load metadata for docker.io/library/maven:3.9-eclipse-temurin-21
#2 DONE 1.4s
#3 [auth] library/maven:pull token for registry-1.docker.io
#3 DONE 0.0s
#4 [build 1/5] FROM docker.io/library/maven:3.9-eclipse-temurin-21@sha256:abc
#4 resolve docker.io/library/maven:3.9 0.0s done
#5 [internal] load build context
#5 transferring context: 3.1MB 0.2s done
#4 [build 1/5] FROM docker.io/library/maven:3.9-eclipse-temurin-21@sha256:abc
#4 DONE 12.0s
#6 [build 2/5] COPY pom.xml .
#6 CACHED
#7 [build 3/5] RUN mvn -q dependency:go-offline
#7 2.113 [INFO] Downloading ` + "\x1b[31m" + `red` + "\x1b[0m" + `
#7 DONE 81.2s
#8 [build 4/5] RUN	mvn package` + "\x1b]0;title\x07" + `
#8 DONE 40.0s
#9 exporting to oci image format
#9 DONE 9.1s
`

func TestStepTrackerReportsCustomerStepsOnce(t *testing.T) {
	tr := buildprogress.NewStepTracker(0, 0)
	var got []string
	for _, line := range strings.Split(dockerfileConsole, "\n") {
		if name, ok := tr.Observe(line); ok {
			got = append(got, name)
		}
	}
	want := []string{
		"[build 1/5] FROM docker.io/library/maven:3.9-eclipse-temurin-21@sha256:abc",
		"[build 2/5] COPY pom.xml .",
		"[build 3/5] RUN mvn -q dependency:go-offline",
		"[build 4/5] RUN mvn package]0;title",
		"exporting to oci image format",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("steps =\n%q\nwant\n%q", got, want)
	}
}

func TestStepTrackerRailpackAndBounds(t *testing.T) {
	tr := buildprogress.NewStepTracker(2, 6)
	var got []string
	for _, line := range strings.Split(railpackBuild, "\n") {
		if name, ok := tr.Observe(line); ok {
			got = append(got, name)
		}
	}
	// "[internal]" and "local context" are skipped; two steps at most,
	// names cut to six bytes.
	if want := []string{"[build", "copy p"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("steps = %q, want %q", got, want)
	}
	var nilTracker *buildprogress.StepTracker
	if _, ok := nilTracker.Observe("#1 npm ci"); ok {
		t.Fatal("nil tracker reported a step")
	}
}

func TestLatestStep(t *testing.T) {
	cases := []struct {
		lines []string
		want  string
	}{
		{nil, ""},
		{[]string{"dependency cache cold — installing dependencies"}, ""},
		{[]string{buildprogress.StepLogPrefix + "[1/3] FROM node", "noise", buildprogress.StepLogPrefix + "[2/3] RUN npm ci\n", "tail"}, "[2/3] RUN npm ci"},
		{[]string{buildprogress.StepLogPrefix + "x\x1b[2Jy"}, "x[2Jy"},
		{[]string{buildprogress.StepLogPrefix}, ""},
	}
	for _, tc := range cases {
		if got := buildprogress.LatestStep(tc.lines); got != tc.want {
			t.Errorf("LatestStep(%q) = %q, want %q", tc.lines, got, tc.want)
		}
	}
}

// A stream that starts mid-build sees statuses and output before any name;
// none of them is a step (the stored tail of a production build starts at
// "#21 extracting sha256:…").
func TestStepTrackerSkipsStatusesSeenFirst(t *testing.T) {
	tr := buildprogress.NewStepTracker(0, 0)
	for _, line := range []string{
		"#21 extracting sha256:4750d048c746b264 0.6s done",
		"#22 preparing build cache for export 26.0s done",
		"#23 writing layer sha256:135e54f6470786b0",
		"#24 sending cache export",
		"#25 0.512 npm warn deprecated glob@7",
		"#26 DONE 12.0s",
		"#27 CACHED",
		"#28 sha256:aaa 12.6MB / 50.1MB 2.0s",
		"#29 naming to docker.io/library/app done",
		"#30 resolve docker.io/library/node:22 0.0s done",
	} {
		if name, ok := tr.Observe(line); ok {
			t.Errorf("status %q reported as step %q", line, name)
		}
	}
	if name, ok := tr.Observe("#31 [build 2/3] RUN npm ci"); !ok || name != "[build 2/3] RUN npm ci" {
		t.Fatalf("real step after statuses = %q, %v", name, ok)
	}
}
