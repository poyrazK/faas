// adr: 1790 — image runs carry the image process and -u/-w into job.json.
package fcvm

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestJobColdBootSpecValidateImageProcess(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*JobColdBootSpec)
		ok     bool
	}{
		{name: "ordinary job", ok: true},
		{name: "image process", mutate: func(s *JobColdBootSpec) { s.ImageProcess = true }, ok: true},
		{name: "image process with overrides", mutate: func(s *JobColdBootSpec) {
			s.ImageProcess, s.ProcessUser, s.ProcessWorkdir = true, "app:staff", "/srv/data"
		}, ok: true},
		{name: "overrides without image process", mutate: func(s *JobColdBootSpec) { s.ProcessUser = "app" }},
		{name: "workdir without image process", mutate: func(s *JobColdBootSpec) { s.ProcessWorkdir = "/srv" }},
		{name: "user with whitespace", mutate: func(s *JobColdBootSpec) { s.ImageProcess, s.ProcessUser = true, "a b" }},
		{name: "user too long", mutate: func(s *JobColdBootSpec) { s.ImageProcess, s.ProcessUser = true, strings.Repeat("u", 257) }},
		{name: "relative workdir", mutate: func(s *JobColdBootSpec) { s.ImageProcess, s.ProcessWorkdir = true, "srv" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec := validInteractiveJobSpec()
			spec.Interactive = nil
			if tc.mutate != nil {
				tc.mutate(&spec)
			}
			if err := spec.Validate(); (err == nil) != tc.ok {
				t.Fatalf("Validate() = %v, want ok=%t", err, tc.ok)
			}
		})
	}
}

// guest-init decodes job.json with unknown fields disallowed, so these names
// are the guest contract (guest/init/job_supervisor_common.go).
func TestJobManifestImageProcessWireShape(t *testing.T) {
	for _, tc := range []struct {
		name     string
		manifest JobManifest
		want     []string
		absent   []string
	}{
		{name: "ordinary", absent: []string{"image_process", `"user"`, "workdir"}},
		{name: "image defaults", manifest: JobManifest{ImageProcess: true},
			want: []string{`"image_process":true`}, absent: []string{`"user"`, "workdir"}},
		{name: "overrides", manifest: JobManifest{ImageProcess: true, User: "1000:0", Workdir: "/srv"},
			want: []string{`"image_process":true`, `"user":"1000:0"`, `"workdir":"/srv"`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := tc.manifest
			m.Kind, m.Command = "job", []string{"/bin/sh"}
			raw, err := json.Marshal(m)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range tc.want {
				if !strings.Contains(string(raw), want) {
					t.Fatalf("manifest %s lacks %s", raw, want)
				}
			}
			for _, absent := range tc.absent {
				if strings.Contains(string(raw), absent) {
					t.Fatalf("manifest %s carries %s", raw, absent)
				}
			}
		})
	}
}

func TestBootJobCarriesImageProcessToSpec(t *testing.T) {
	vmm := &fakeVMM{}
	m := newTestManager(&fakeRunner{}, vmm)
	req := testJobBootRequest("job-image-process")
	req.ImageProcess, req.ProcessUser, req.ProcessWorkdir = true, "app", "/srv"
	if _, err := m.BootJob(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	vmm.mu.Lock()
	specs := append([]JobColdBootSpec(nil), vmm.jobBootSpecs...)
	vmm.mu.Unlock()
	if len(specs) != 1 || !specs[0].ImageProcess || specs[0].ProcessUser != "app" || specs[0].ProcessWorkdir != "/srv" {
		t.Fatalf("job boot specs = %+v", specs)
	}
}
