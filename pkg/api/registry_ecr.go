package api

import "regexp"

// ECRRegistryHost matches an Amazon ECR registry host. Its groups are the
// account, the FIPS suffix, the region and the China partition suffix
// (ADR-20261025124445).
var ECRRegistryHost = regexp.MustCompile(`^([0-9]{12})\.dkr\.ecr(-fips)?\.([a-z0-9-]+)\.amazonaws\.com(\.cn)?$`)

var awsAccessKeyID = regexp.MustCompile(`^AKIA[A-Z0-9]{16}$`)

// IsECRAccessKey reports whether a registry credential is an IAM access key
// for an ECR registry. Pulls exchange such a key for ECR's registry password
// and never send the key itself to a registry.
func IsECRAccessKey(registry, username string) bool {
	return awsAccessKeyID.MatchString(username) && ECRRegistryHost.MatchString(registry)
}
