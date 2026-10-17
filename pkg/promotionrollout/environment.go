package promotionrollout

import (
	"path/filepath"
)

const (
	PolicyEnv        = "FAAS_PROMOTION_ROLLOUT_POLICY"
	TrustKeyEnv      = "FAAS_PROMOTION_ROLLOUT_TRUST_KEY"
	QualificationEnv = "FAAS_PROMOTION_ROLLOUT_QUALIFICATION"
)

func FromEnv(getenv func(string) string, revision string) *Gate {
	enabled := map[Capability]bool{}
	for capability, name := range map[Capability]string{Secrets: "FAAS_PROJECT_RELEASE_SECRETS_ENABLED", Health: "FAAS_PROMOTION_HEALTH_ENABLED", ServiceGraph: "FAAS_PROMOTION_SERVICE_GRAPH_ENABLED", Workers: "FAAS_PROMOTION_WORKERS_ENABLED", Migrations: "FAAS_PROMOTION_MIGRATIONS_ENABLED"} {
		enabled[capability] = getenv(name) == "1"
	}
	gate := New(Config{PolicyPath: getenv(PolicyEnv), SourceSHA: revision, Enabled: enabled})
	if value := getenv(QualificationEnv); value != "" && value != "0" {
		if getenv(QualificationEnv) == "1" && nativeQualificationHost(getenv("DATABASE_URL")) {
			gate.qualification = true
		} else {
			gate.bootstrapReason = "qualification_host_invalid"
		}
		return gate
	}
	if !filepath.IsAbs(getenv(TrustKeyEnv)) {
		gate.bootstrapReason = "trust_key_unavailable"
		return gate
	}
	raw, err := ReadFile(getenv(TrustKeyEnv), 1024)
	if err != nil {
		gate.bootstrapReason = "trust_key_unavailable"
		return gate
	}
	gate.config.PublicKey, err = PublicKey(raw)
	if err != nil {
		gate.bootstrapReason = "trust_key_invalid"
	}
	return gate
}
