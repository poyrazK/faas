// adr: 20261024225612 — CPU templates: host classification, template safety
// and the restore-compatibility rule.
package fcvm

import (
	"encoding/json"
	"strings"
	"testing"
)

func cpuinfo(vendor string, family, model, stepping int, flags string) []byte {
	block := func(n int) string {
		return strings.Join([]string{
			"processor\t: " + itoa(n),
			"vendor_id\t: " + vendor,
			"cpu family\t: " + itoa(family),
			"model\t\t: " + itoa(model),
			"model name\t: test cpu",
			"stepping\t: " + itoa(stepping),
			"flags\t\t: " + flags,
			"",
		}, "\n")
	}
	return []byte(block(0) + "\n" + block(1))
}

func TestParseHostCPUClassifiesLaunchCandidates(t *testing.T) {
	cases := []struct {
		name     string
		vendor   string
		family   int
		model    int
		stepping int
		want     CPUClass
	}{
		{"SYS-5 Xeon Silver 4214R", "GenuineIntel", 6, 85, 7, CPUClassCascadeLake},
		{"SYS-2 Xeon-D 2141I", "GenuineIntel", 6, 85, 4, CPUClassSkylake},
		{"Cooper Lake", "GenuineIntel", 6, 85, 11, CPUClassCooperLake},
		{"RISE-6 Xeon Gold 6312U", "GenuineIntel", 6, 106, 6, CPUClassIceLake},
		{"Ice Lake-D", "GenuineIntel", 6, 108, 1, CPUClassIceLake},
		{"Sapphire Rapids", "GenuineIntel", 6, 143, 8, CPUClassSapphireRapids},
		{"RISE-2 Xeon-E 2388G Rocket Lake", "GenuineIntel", 6, 167, 1, CPUClassUnknown},
		{"EPYC Milan", "AuthenticAMD", 25, 1, 1, CPUClassMilan},
		{"EPYC Genoa", "AuthenticAMD", 25, 17, 1, CPUClassGenoa},
		{"EPYC Naples", "AuthenticAMD", 23, 1, 2, CPUClassNaples},
		{"Ryzen 9700X", "AuthenticAMD", 26, 68, 0, CPUClassUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, err := ParseHostCPU(cpuinfo(tc.vendor, tc.family, tc.model, tc.stepping, "fpu sse avx2"))
			if err != nil {
				t.Fatalf("ParseHostCPU: %v", err)
			}
			if h.Class != tc.want {
				t.Fatalf("class = %q, want %q", h.Class, tc.want)
			}
			if h.Vendor != tc.vendor || h.Family != tc.family || h.Model != tc.model || h.Stepping != tc.stepping {
				t.Fatalf("parsed %+v", h)
			}
		})
	}
}

func TestParseHostCPURejectsIncompleteInput(t *testing.T) {
	if _, err := ParseHostCPU([]byte("vendor_id\t: GenuineIntel\ncpu family\t: 6\n")); err == nil {
		t.Fatal("expected an error for a block without model, stepping and flags")
	}
	if _, err := ParseHostCPU([]byte("vendor_id\t: GenuineIntel\ncpu family\t: six\nmodel\t: 85\nstepping\t: 7\nflags\t: fpu\n")); err == nil {
		t.Fatal("expected an error for a non-numeric family")
	}
}

func TestHostCPUSignatureIsStableAndFlagSensitive(t *testing.T) {
	a, _ := ParseHostCPU(cpuinfo("GenuineIntel", 6, 85, 7, "fpu avx512f sse"))
	b, _ := ParseHostCPU(cpuinfo("GenuineIntel", 6, 85, 7, "sse fpu avx512f fpu"))
	c, _ := ParseHostCPU(cpuinfo("GenuineIntel", 6, 85, 7, "fpu avx512f sse md_clear"))
	if a.Signature() != b.Signature() {
		t.Fatalf("flag order or duplicates changed the signature: %q vs %q", a.Signature(), b.Signature())
	}
	if a.Signature() == c.Signature() {
		t.Fatal("a different flag set kept the same signature")
	}
	if !strings.HasPrefix(a.Signature(), "cascadelake/GenuineIntel-6-85-7/") {
		t.Fatalf("signature = %q", a.Signature())
	}
	if got := CPUSignatureClass(a.Signature()); got != CPUClassCascadeLake {
		t.Fatalf("CPUSignatureClass = %q", got)
	}
	if got := CPUSignatureClass(""); got != CPUClassUnknown {
		t.Fatalf("empty signature class = %q", got)
	}
}

func TestValidateCPUTemplate(t *testing.T) {
	cascade, _ := ParseHostCPU(cpuinfo("GenuineIntel", 6, 85, 7, "fpu"))
	ice, _ := ParseHostCPU(cpuinfo("GenuineIntel", 6, 106, 6, "fpu"))
	rocket, _ := ParseHostCPU(cpuinfo("GenuineIntel", 6, 167, 1, "fpu"))
	milan, _ := ParseHostCPU(cpuinfo("AuthenticAMD", 25, 1, 1, "fpu"))
	for _, ok := range []struct {
		template string
		host     HostCPU
	}{{"", rocket}, {"T2CL", cascade}, {"T2CL", ice}, {"T2S", cascade}, {"T2A", milan}} {
		if err := ValidateCPUTemplate(ok.template, ok.host); err != nil {
			t.Errorf("ValidateCPUTemplate(%q, %s) = %v", ok.template, ok.host.Class, err)
		}
	}
	for _, bad := range []struct {
		template string
		host     HostCPU
	}{{"T2CL", rocket}, {"T2CL", milan}, {"T2S", ice}, {"T2A", cascade}, {"t2cl", cascade}, {"None", cascade}} {
		if err := ValidateCPUTemplate(bad.template, bad.host); err == nil {
			t.Errorf("ValidateCPUTemplate(%q, %s) accepted", bad.template, bad.host.Class)
		}
	}
	if ValidCPUTemplate("V1N1") || !ValidCPUTemplate("") || !ValidCPUTemplate("T2CL") {
		t.Fatal("ValidCPUTemplate table drifted")
	}
}

func TestSnapshotRestoreCompatible(t *testing.T) {
	cascade, _ := ParseHostCPU(cpuinfo("GenuineIntel", 6, 85, 7, "fpu avx512f"))
	ice, _ := ParseHostCPU(cpuinfo("GenuineIntel", 6, 106, 6, "fpu avx512f gfni vaes"))
	rocket, _ := ParseHostCPU(cpuinfo("GenuineIntel", 6, 167, 1, "fpu avx512f"))
	cases := []struct {
		name               string
		template, captured string
		node               string
		want               bool
	}{
		{"T2CL capture restores on Cascade Lake", "T2CL", ice.Signature(), cascade.Signature(), true},
		{"T2CL capture restores on Ice Lake", "T2CL", cascade.Signature(), ice.Signature(), true},
		{"T2CL capture never restores on Rocket Lake", "T2CL", cascade.Signature(), rocket.Signature(), false},
		{"T2CL capture never restores on an unregistered node", "T2CL", cascade.Signature(), "", false},
		{"template-less capture restores on the identical host", "", ice.Signature(), ice.Signature(), true},
		{"template-less Ice Lake capture never restores on Cascade Lake", "", ice.Signature(), cascade.Signature(), false},
		{"legacy row is left to the caller", "", "", cascade.Signature(), false},
	}
	for _, tc := range cases {
		if got := SnapshotRestoreCompatible(tc.template, tc.captured, tc.node); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestWithCPUTemplateSetsMachineConfig(t *testing.T) {
	cfg := BuildColdBootConfig(ColdBootSpec{KernelKey: "k", BaseKey: "b", LayerKey: "l", VcpuCount: 2, MemSizeMiB: 256, Tap: "tap0"}, 1)
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "cpu_template") {
		t.Fatalf("a VMM without a template must omit the field: %s", raw)
	}
	v := (&JailerVMM{}).WithCPUTemplate("T2CL")
	got := v.withCPUTemplate(cfg)
	raw, err = json.Marshal(got.MachineConfig)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"cpu_template":"T2CL"`) {
		t.Fatalf("machine-config = %s", raw)
	}
	cfg.MachineConfig.CPUTemplate = "T2S"
	if got := v.withCPUTemplate(cfg); got.MachineConfig.CPUTemplate != "T2S" {
		t.Fatalf("a caller-chosen template was overwritten: %q", got.MachineConfig.CPUTemplate)
	}
	if got := (&JailerVMM{}).withCPUTemplate(BuildColdBootConfig(ColdBootSpec{KernelKey: "k", BaseKey: "b", LayerKey: "l", VcpuCount: 1, MemSizeMiB: 128, Tap: "tap0"}, 1)); got.MachineConfig.CPUTemplate != "" {
		t.Fatalf("no template configured but got %q", got.MachineConfig.CPUTemplate)
	}
}
