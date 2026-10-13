package api

import "regexp"

var objectStorageBindingPrefixPattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,47}$`)

// ValidObjectStorageBindingPrefix matches the prefix contract for the six
// injected managed object-storage environment variables.
func ValidObjectStorageBindingPrefix(prefix string) bool {
	return objectStorageBindingPrefixPattern.MatchString(prefix)
}

type ObjectStorageBindingProbeCheck struct {
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// ObjectStorageBindingProbeReport excludes endpoint URLs, object names,
// credentials and provider errors. Empty Mode is the legacy listing canary;
// write and read_write are explicitly requested disposable-object probes.
// No mode establishes resident application adoption.
type ObjectStorageBindingProbeReport struct {
	App           string                          `json:"app,omitempty"`
	Prefix        string                          `json:"prefix,omitempty"`
	TaskID        string                          `json:"task_id,omitempty"`
	DeploymentID  string                          `json:"deployment_id,omitempty"`
	Environment   ObjectStorageBindingProbeCheck  `json:"environment"`
	Configuration ObjectStorageBindingProbeCheck  `json:"configuration"`
	Connection    ObjectStorageBindingProbeCheck  `json:"connection"`
	Authorization ObjectStorageBindingProbeCheck  `json:"authorization"`
	BucketAccess  ObjectStorageBindingProbeCheck  `json:"bucket_access"`
	Mode          string                          `json:"mode,omitempty"`
	Upload        *ObjectStorageBindingProbeCheck `json:"upload,omitempty"`
	Download      *ObjectStorageBindingProbeCheck `json:"download,omitempty"`
	Cleanup       *ObjectStorageBindingProbeCheck `json:"cleanup,omitempty"`
	Error         string                          `json:"error,omitempty"`
}

func (r ObjectStorageBindingProbeReport) Passed() bool {
	base := r.Error == "" && r.Environment.Status == "passed" && r.Configuration.Status == "passed" &&
		r.Connection.Status == "passed" && r.Authorization.Status == "passed" && r.BucketAccess.Status == "passed"
	if r.Mode == "" {
		return base && r.Upload == nil && r.Download == nil && r.Cleanup == nil
	}
	return base && ValidObjectStorageWriteProbeMode(r.Mode) && r.Upload != nil && r.Upload.Status == "passed" &&
		r.Cleanup != nil && r.Cleanup.Status == "passed" && r.Download != nil &&
		(r.Mode == "read_write" && r.Download.Status == "passed" || r.Mode == "write" && r.Download.Status == "not_checked")
}

func ValidObjectStorageWriteProbeMode(mode string) bool {
	return mode == "write" || mode == "read_write"
}
