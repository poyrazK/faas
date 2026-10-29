package fcvm

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
)

// CPU templates and host CPU identity (ADR-20261024225612).
//
// Firecracker only promises that a snapshot resumes on hardware identical to
// the host that captured it: a guest picks code paths from CPUID at boot, so
// a restore onto a CPU missing one of those instructions can fail to load or
// crash later. A CPU template trims the CPUID a guest sees to a baseline that
// several CPU generations share. vmmd boots every guest with its node's
// template, records the node's identity in compute_nodes, and schedd restores
// a snapshot only on a node whose CPU can carry what the snapshot recorded.

// CPUClass is a host CPU microarchitecture, the unit Firecracker's template
// table is written in.
type CPUClass string

const (
	CPUClassSkylake        CPUClass = "skylake"
	CPUClassCascadeLake    CPUClass = "cascadelake"
	CPUClassCooperLake     CPUClass = "cooperlake"
	CPUClassIceLake        CPUClass = "icelake"
	CPUClassSapphireRapids CPUClass = "sapphirerapids"
	CPUClassEmeraldRapids  CPUClass = "emeraldrapids"
	CPUClassNaples         CPUClass = "naples"
	CPUClassRome           CPUClass = "rome"
	CPUClassMilan          CPUClass = "milan"
	CPUClassGenoa          CPUClass = "genoa"
	CPUClassTurin          CPUClass = "turin"
	// CPUClassUnknown is every CPU this table does not name. No template
	// supports it: a guess would be a restore crash waiting to happen.
	CPUClassUnknown CPUClass = "unknown"
)

// cpuTemplateClasses is Firecracker v1.17.0's static template table
// (docs/cpu_templates/cpu-templates.md): the CPU classes each template is
// documented as safe on. V1N1 is aarch64 only and Gregale is x86_64 only.
var cpuTemplateClasses = map[string][]CPUClass{
	"C3":   {CPUClassSkylake, CPUClassCascadeLake, CPUClassIceLake},
	"T2":   {CPUClassSkylake, CPUClassCascadeLake, CPUClassIceLake},
	"T2S":  {CPUClassSkylake, CPUClassCascadeLake},
	"T2CL": {CPUClassCascadeLake, CPUClassIceLake},
	"T2A":  {CPUClassMilan},
}

// CPUTemplateNames lists the templates vmmd accepts, sorted.
func CPUTemplateNames() []string {
	names := make([]string, 0, len(cpuTemplateClasses))
	for name := range cpuTemplateClasses {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// ValidCPUTemplate reports whether name is a known template or empty (none).
func ValidCPUTemplate(name string) bool {
	if name == "" {
		return true
	}
	_, ok := cpuTemplateClasses[name]
	return ok
}

// CPUTemplateSupports reports whether template is documented as safe on a
// CPU of class. The empty template supports no class: a template-less
// snapshot is tied to its exact host signature instead.
func CPUTemplateSupports(template string, class CPUClass) bool {
	return slices.Contains(cpuTemplateClasses[template], class)
}

// HostCPU is the first processor block of /proc/cpuinfo.
type HostCPU struct {
	Vendor   string
	Family   int
	Model    int
	Stepping int
	Flags    []string
	Class    CPUClass
}

// ReadHostCPU reads and classifies this host's CPU.
func ReadHostCPU() (HostCPU, error) {
	b, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		return HostCPU{}, fmt.Errorf("fcvm: read host cpu: %w", err)
	}
	return ParseHostCPU(b)
}

// ParseHostCPU parses the first processor block of a /proc/cpuinfo dump.
// Every core of a single-socket or homogeneous multi-socket host reports the
// same identity, so the first block stands for the host.
func ParseHostCPU(cpuinfo []byte) (HostCPU, error) {
	var h HostCPU
	seen := map[string]bool{}
	sc := bufio.NewScanner(bytes.NewReader(cpuinfo))
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "" {
			if len(seen) > 0 {
				break
			}
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		var err error
		switch key {
		case "vendor_id":
			h.Vendor = value
		case "cpu family":
			h.Family, err = strconv.Atoi(value)
		case "model":
			h.Model, err = strconv.Atoi(value)
		case "stepping":
			h.Stepping, err = strconv.Atoi(value)
		case "flags":
			h.Flags = strings.Fields(value)
		default:
			continue
		}
		if err != nil {
			return HostCPU{}, fmt.Errorf("fcvm: parse host cpu %q: %w", key, err)
		}
		seen[key] = true
	}
	if err := sc.Err(); err != nil {
		return HostCPU{}, fmt.Errorf("fcvm: parse host cpu: %w", err)
	}
	for _, key := range []string{"vendor_id", "cpu family", "model", "stepping", "flags"} {
		if !seen[key] {
			return HostCPU{}, fmt.Errorf("fcvm: parse host cpu: missing %q", key)
		}
	}
	h.Class = classifyCPU(h.Vendor, h.Family, h.Model, h.Stepping)
	return h, nil
}

// classifyCPU maps vendor, family, model and stepping to a class. Cascade
// Lake shares Skylake-SP's model number and differs by stepping.
func classifyCPU(vendor string, family, model, stepping int) CPUClass {
	switch vendor {
	case "GenuineIntel":
		if family != 6 {
			return CPUClassUnknown
		}
		switch model {
		case 85:
			switch {
			case stepping <= 4:
				return CPUClassSkylake
			case stepping <= 7:
				return CPUClassCascadeLake
			case stepping == 10 || stepping == 11:
				return CPUClassCooperLake
			}
		case 106, 108:
			return CPUClassIceLake
		case 143:
			return CPUClassSapphireRapids
		case 207:
			return CPUClassEmeraldRapids
		}
	case "AuthenticAMD":
		switch {
		case family == 23 && model == 1:
			return CPUClassNaples
		case family == 23 && model == 49:
			return CPUClassRome
		case family == 25 && model <= 0x0f:
			return CPUClassMilan
		case family == 25 && model >= 0x10 && model <= 0x1f:
			return CPUClassGenoa
		case family == 26 && model <= 0x1f:
			return CPUClassTurin
		}
	}
	return CPUClassUnknown
}

// Signature identifies the host exactly enough for a template-less snapshot:
// class, vendor, family, model, stepping and a hash of the flag set, e.g.
// "cascadelake/GenuineIntel-6-85-7/3f2a9c1b04de". A kernel or microcode
// change that alters the flags changes the signature; template-less
// snapshots then cold-boot once rather than risk a mismatch.
func (h HostCPU) Signature() string {
	flags := slices.Clone(h.Flags)
	slices.Sort(flags)
	flags = slices.Compact(flags)
	sum := sha256.Sum256([]byte(strings.Join(flags, " ")))
	return fmt.Sprintf("%s/%s-%d-%d-%d/%s", h.Class, h.Vendor, h.Family, h.Model, h.Stepping, hex.EncodeToString(sum[:6]))
}

// CPUSignatureClass returns the class a Signature starts with; an empty or
// malformed signature is CPUClassUnknown.
func CPUSignatureClass(signature string) CPUClass {
	class, _, ok := strings.Cut(signature, "/")
	if !ok || class == "" {
		return CPUClassUnknown
	}
	return CPUClass(class)
}

// ValidateCPUTemplate refuses a template the host's class is not documented
// as safe for. The empty template is always valid.
func ValidateCPUTemplate(template string, host HostCPU) error {
	if template == "" {
		return nil
	}
	classes, ok := cpuTemplateClasses[template]
	if !ok {
		return fmt.Errorf("fcvm: unknown cpu template %q (supported: %s)", template, strings.Join(CPUTemplateNames(), ", "))
	}
	if !slices.Contains(classes, host.Class) {
		return fmt.Errorf("fcvm: cpu template %q is not safe on this host (%s, %s family %d model %d stepping %d); it supports %v",
			template, host.Class, host.Vendor, host.Family, host.Model, host.Stepping, classes)
	}
	return nil
}

// SnapshotRestoreCompatible reports whether a snapshot captured under
// (snapTemplate, snapModel) can restore on a node whose host signature is
// nodeModel. A templated snapshot needs a node class the template supports,
// whatever template that node boots new guests with; a template-less one
// needs the identical signature. A snapshot without a recorded model is a
// legacy row: callers decide those with fleet context.
func SnapshotRestoreCompatible(snapTemplate, snapModel, nodeModel string) bool {
	if snapTemplate != "" {
		return CPUTemplateSupports(snapTemplate, CPUSignatureClass(nodeModel))
	}
	return snapModel != "" && snapModel == nodeModel
}

// WithCPUTemplate boots every guest this VMM starts with template
// (ADR-20261024225612). Restores keep the CPUID their snapshot recorded. The
// caller must have checked ValidateCPUTemplate against the host.
func (v *JailerVMM) WithCPUTemplate(template string) *JailerVMM {
	v.cpuTemplate = template
	return v
}

// withCPUTemplate returns cfg with the VMM's template set, unless the caller
// already chose one.
func (v *JailerVMM) withCPUTemplate(cfg VMConfig) VMConfig {
	if v.cpuTemplate != "" && cfg.MachineConfig.CPUTemplate == "" {
		cfg.MachineConfig.CPUTemplate = v.cpuTemplate
	}
	return cfg
}
