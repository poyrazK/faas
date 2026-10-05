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

// ObjectStorageBindingProbeReport is a bounded read-access canary report. It
// excludes endpoint URLs, bucket contents, credentials and provider errors.
// A passing report does not establish write access or resident VM adoption.
type ObjectStorageBindingProbeReport struct {
	App           string                         `json:"app,omitempty"`
	Prefix        string                         `json:"prefix,omitempty"`
	TaskID        string                         `json:"task_id,omitempty"`
	DeploymentID  string                         `json:"deployment_id,omitempty"`
	Environment   ObjectStorageBindingProbeCheck `json:"environment"`
	Configuration ObjectStorageBindingProbeCheck `json:"configuration"`
	Connection    ObjectStorageBindingProbeCheck `json:"connection"`
	Authorization ObjectStorageBindingProbeCheck `json:"authorization"`
	BucketAccess  ObjectStorageBindingProbeCheck `json:"bucket_access"`
	Error         string                         `json:"error,omitempty"`
}

func (r ObjectStorageBindingProbeReport) Passed() bool {
	return r.Error == "" && r.Environment.Status == "passed" && r.Configuration.Status == "passed" &&
		r.Connection.Status == "passed" && r.Authorization.Status == "passed" && r.BucketAccess.Status == "passed"
}
