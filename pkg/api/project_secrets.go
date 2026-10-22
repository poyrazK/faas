package api

// ProjectSecretsMaxJSONBytes bounds the encoded multipart bundle using the
// existing per-app quotas. A JSON byte can need six bytes when escaped.
func ProjectSecretsMaxJSONBytes(limits Limits) int64 {
	return int64(limits.SecretCountMax)*(int64(limits.SecretValueMaxBytes)*6+MaxSecretKeyLen+16) + 2
}
