package api

import (
	"net/http"
	"strconv"
)

// ResourceProfile is a named, versioned resource shape for an app instance.
// Profiles deliberately resolve to the existing RAM and sustained CPU knobs so
// placement, billing, and cgroup enforcement keep one source of truth.
type ResourceProfile string

const (
	ResourceProfileMicro  ResourceProfile = "micro"
	ResourceProfileSmall  ResourceProfile = "small"
	ResourceProfileMedium ResourceProfile = "medium"
	ResourceProfileLarge  ResourceProfile = "large"
	ResourceProfileXLarge ResourceProfile = "xlarge"
)

// ResourceProfileSpec is the resolved shape behind a named profile.
type ResourceProfileSpec struct {
	Name          ResourceProfile
	MemoryMB      int
	CPUMillicores int
}

// ResourceProfiles is the public, closed set. Keep this table ordered from
// smallest to largest so API documentation and CLI help can enumerate it
// deterministically.
var ResourceProfiles = []ResourceProfileSpec{
	{Name: ResourceProfileMicro, MemoryMB: 128, CPUMillicores: 250},
	{Name: ResourceProfileSmall, MemoryMB: 256, CPUMillicores: 500},
	{Name: ResourceProfileMedium, MemoryMB: 512, CPUMillicores: 1000},
	{Name: ResourceProfileLarge, MemoryMB: 768, CPUMillicores: 1000},
	{Name: ResourceProfileXLarge, MemoryMB: 1024, CPUMillicores: 1000},
}

// ResourceProfileSpecFor resolves a profile name. The bool is false for an
// empty or unknown name so callers can distinguish omission from a typo.
func ResourceProfileSpecFor(name string) (ResourceProfileSpec, bool) {
	for _, profile := range ResourceProfiles {
		if string(profile.Name) == name {
			return profile, true
		}
	}
	return ResourceProfileSpec{}, false
}

// ResourceProfileForResources returns the named profile matching a resolved
// memory/CPU pair, or the empty string for a custom shape.
func ResourceProfileForResources(memoryMB, cpuMillicores int) ResourceProfile {
	for _, profile := range ResourceProfiles {
		if profile.MemoryMB == memoryMB && profile.CPUMillicores == cpuMillicores {
			return profile.Name
		}
	}
	return ""
}

// ErrInvalidResourceProfile returns the stable validation problem for an
// unknown profile name.
func ErrInvalidResourceProfile(name string) *Problem {
	return NewProblem(http.StatusUnprocessableEntity, CodeInvalidResourceProfile,
		"Invalid resource profile",
		"resource_profile must be one of: micro, small, medium, large, xlarge")
}

// ErrResourceProfileConflict reports a request that combines a named profile
// with a conflicting explicit memory or CPU value.
func ErrResourceProfileConflict(field string, profile ResourceProfile, want, got int) *Problem {
	return NewProblem(http.StatusUnprocessableEntity, CodeInvalidResourceProfile,
		"Conflicting resource profile",
		field+" conflicts with resource_profile "+string(profile)+" (profile value "+strconv.Itoa(want)+", requested "+strconv.Itoa(got)+")")
}
