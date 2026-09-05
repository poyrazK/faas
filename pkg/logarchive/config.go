// config.go — runtime configuration for the log archive shipper
// (issue #562). All values are env-var driven (sealed at rest per
// ADR-020) with safe fallbacks for the disabled case
// (FAAS_LOG_ARCHIVE_BUCKET unset → shipper wired but exits
// immediately on ctx.Done()).
//
// Mirrors pkg/eventretention.Params: required fields are explicit,
// optional fields have zero-value fallbacks, and the New constructor
// is the single entry point the daemon lifecycle uses. Tests
// drive RunOnce directly with explicit durations.

package logarchive

import (
	"fmt"
	"log/slog"
	"os"
	"time"
)

// DefaultFlushInterval is the cadence the shipper scans the spool
// dir and pushes any .jsonl.partial or retry .jsonl.upload files to S3. 5 minutes matches
// the issue #562 acceptance criterion 4 ("no log loss in 5-min
// window between flushes"). The interval is overridable via
// FAAS_LOG_ARCHIVE_INTERVAL for tests + the per-deploy drop-in.
const DefaultFlushInterval = 5 * time.Minute

// DefaultPurgeInterval is the cadence the shipper removes
// .jsonl.gz files older than the retention boundary. Daily matches
// pkg/eventretention's cadence — the per-tick cost is bounded by
// the per-day directory count, not the per-line count, so a 24h
// tick is comfortably cheap.
const DefaultPurgeInterval = 24 * time.Hour

// DefaultRetentionDays is the local-spool retention boundary
// (issue #562 acceptance criterion 1). 7 days matches the issue's
// proposed value; FAAS_LOG_ARCHIVE_RETENTION_DAYS overrides per
// deploy. The value is upper-bounded by
// api.LogArchiveRetentionDaysMax so an operator can't accidentally
// fill the local volume.
const DefaultRetentionDays = 7

// DefaultLocalBytesMax is the upper bound on the local spool
// size. The shipper checks this BEFORE opening a new .partial
// file and refuses to write if it would exceed the cap (issue
// #562 risk #6: hidden size growth on the local spool during a
// multi-day bucket outage). 10 GB matches the typical per-day
// uncompressed volume for a 1000 rps app × 50 KB/s; the cap is
// overridable via FAAS_LOG_ARCHIVE_LOCAL_BYTES_MAX.
const DefaultLocalBytesMax = 10 << 30

// EnvBucket is the FAAS_LOG_ARCHIVE_BUCKET env var — required for
// the shipper to run. Empty = shipper exits on ctx.Done() (the
// disabled-path branch in cmd/apid/main.go).
const EnvBucket = "FAAS_LOG_ARCHIVE_BUCKET"

// EnvEndpoint is the FAAS_LOG_ARCHIVE_ENDPOINT env var — the
// S3-compatible endpoint URL (e.g. https://<accountid>.r2.cloudflarestorage.com
// or https://s3.us-east-1.amazonaws.com). Defaults to AWS S3 if
// unset, but the shipper fails fast on empty bucket so an operator
// who only sets Endpoint without Bucket still gets a clear error.
const EnvEndpoint = "FAAS_LOG_ARCHIVE_ENDPOINT"

// EnvRegion is the FAAS_LOG_ARCHIVE_REGION env var — SigV4 signing
// region. Defaults to "us-east-1" if unset (AWS's default region
// for SigV4 canonical request signing).
const EnvRegion = "FAAS_LOG_ARCHIVE_REGION"

// EnvKeyID is the FAAS_LOG_ARCHIVE_KEY_ID env var — the S3
// access key id. ADR-020 sealed-at-rest: delivered via the
// archive-creds.json envelope unsealed by `gregale backup
// unseal-archive-creds`, mounted at /etc/faas/secrets/storage-box/
// archive-creds.json via systemd LoadCredential=. Empty = no creds,
// shipper fails closed.
const EnvKeyID = "FAAS_LOG_ARCHIVE_KEY_ID"

// EnvSecret is the FAAS_LOG_ARCHIVE_SECRET env var — the S3 secret
// access key. Same sealed-at-rest path as EnvKeyID.
const EnvSecret = "FAAS_LOG_ARCHIVE_SECRET"

// EnvInterval is the FAAS_LOG_ARCHIVE_INTERVAL env var — flush
// cadence override. Empty = DefaultFlushInterval. Unparseable =
// Warn + fallback (matches graceIntervalFromEnv pattern at
// cmd/apid/main.go:407).
const EnvInterval = "FAAS_LOG_ARCHIVE_INTERVAL"

// EnvRetentionDays is the FAAS_LOG_ARCHIVE_RETENTION_DAYS env var
// — local-spool retention boundary override. Empty =
// DefaultRetentionDays. Upper-bounded by the per-deploy cap (see
// Plan.LogArchiveRetentionDaysMax).
const EnvRetentionDays = "FAAS_LOG_ARCHIVE_RETENTION_DAYS"

// EnvLocalBytesMax is the FAAS_LOG_ARCHIVE_LOCAL_BYTES_MAX env
// var — local-spool size cap override. Empty = DefaultLocalBytesMax.
const EnvLocalBytesMax = "FAAS_LOG_ARCHIVE_LOCAL_BYTES_MAX"

// EnvSpoolRoot is the FAAS_LOG_ARCHIVE_SPOOL_ROOT env var —
// directory the spool writes into. Empty = DefaultSpoolRoot.
// Must be on a ReadWritePaths-allowed path (apid's systemd unit
// already grants /var/log/faas).
const EnvSpoolRoot = "FAAS_LOG_ARCHIVE_SPOOL_ROOT"

// EnvVMMDSpoolRoot is an optional vmmd-only override. vmmd uses a
// separate default root from apid because both daemons may run on a
// single-box host and must not race over the same spool files.
const EnvVMMDSpoolRoot = "FAAS_VMMD_LOG_ARCHIVE_SPOOL_ROOT"

// EnvCredentialsPath is the systemd-staged archive credential path. It is
// separate from the S3 value env vars because the file is loaded by PID 1
// and exposed to each daemon as a credential-directory path.
const EnvCredentialsPath = "FAAS_LOG_ARCHIVE_CREDS_PATH"

// DefaultCredentialsPath is used outside systemd (for example by a manual
// binary invocation) and is also the source path used by LoadCredential=.
const DefaultCredentialsPath = "/etc/faas/secrets/storage-box/archive-creds.json"

// DefaultSpoolRoot is the local spool directory the shipper
// owns. Matches the apid systemd unit's ReadWritePaths entry
// (/var/log/faas). Tests override via Params.SpoolRoot.
const DefaultSpoolRoot = "/var/log/faas/archive"

// DefaultVMMDSpoolRoot keeps the compute-side producer and control-plane
// shipper independent on single-box deployments. The bucket key remains
// unchanged, so the gateway read-back path does not need to know which
// daemon produced the object.
const DefaultVMMDSpoolRoot = "/var/log/faas/vmmd-archive"

// Config is the runtime configuration the Shipper reads from.
// All fields are public so tests construct directly without
// touching the env. The production wire-up goes through
// ConfigFromEnv so a config-error fails closed at boot.
type Config struct {
	// Endpoint is the S3-compatible base URL (no trailing slash).
	// SigV4-signed PUTs hit {Endpoint}/{bucket}/{key}.
	Endpoint string
	// Region is the SigV4 signing region (e.g. "us-east-1",
	// "auto" for R2).
	Region string
	// Bucket is the destination bucket name. Empty Config =
	// disabled mode; Shipper.Run returns nil immediately on
	// ctx.Done() without touching S3.
	Bucket string
	// KeyID is the S3 access key id. Paired with Secret; both
	// must be set or the shipper refuses to boot.
	KeyID string
	// Secret is the S3 secret access key.
	Secret string
	// FlushInterval is the per-tick ship cadence. 0 =
	// DefaultFlushInterval.
	FlushInterval time.Duration
	// PurgeInterval is the per-tick purge cadence. 0 =
	// DefaultPurgeInterval.
	PurgeInterval time.Duration
	// RetentionDays is the local-spool retention boundary. 0 =
	// DefaultRetentionDays. Upper-bounded by the per-deploy cap
	// passed to New (api.LogArchiveRetentionDaysMax via the
	// apid wire-up).
	RetentionDays int
	// LocalBytesMax is the upper bound on the local spool size.
	// 0 = DefaultLocalBytesMax.
	LocalBytesMax int64
	// SpoolRoot is the directory the spool writes into. Empty =
	// DefaultSpoolRoot.
	SpoolRoot string

	// regionExplicit distinguishes an explicit FAAS_LOG_ARCHIVE_REGION from
	// ConfigFromEnv's us-east-1 default. It lets an archive envelope provide
	// its region without overriding an operator-provided environment value.
	regionExplicit bool
}

// Enabled reports whether the config has the minimum required
// fields to ship. The only required field is Bucket — empty
// Bucket = disabled. Endpoint/KeyID/Secret failures at runtime
// are returned by RunOnce as typed errors; an operator can stage
// the creds envelope out-of-band and the next daemon restart
// flips the switch without code changes.
func (c Config) Enabled() bool {
	return c.Bucket != ""
}

// ConfigFromEnv reads the FAAS_LOG_ARCHIVE_* env vars and
// returns a Config. The Log arg receives the parse-error Warn
// lines (e.g. unparseable FAAS_LOG_ARCHIVE_INTERVAL falls back
// to DefaultFlushInterval with a Warn); nil Log falls back to
// slog.Default(). Getenv is injectable for tests.
func ConfigFromEnv(getenv func(string) string, log *slog.Logger) (Config, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	if log == nil {
		log = slog.Default()
	}
	cfg := Config{
		Endpoint:       getenv(EnvEndpoint),
		Region:         defaultRegion(getenv(EnvRegion)),
		Bucket:         getenv(EnvBucket),
		KeyID:          getenv(EnvKeyID),
		Secret:         getenv(EnvSecret),
		FlushInterval:  parseDurationEnv(getenv(EnvInterval), DefaultFlushInterval, EnvInterval, log),
		PurgeInterval:  DefaultPurgeInterval,
		RetentionDays:  parseIntEnv(getenv(EnvRetentionDays), DefaultRetentionDays, EnvRetentionDays, log),
		LocalBytesMax:  parseInt64Env(getenv(EnvLocalBytesMax), DefaultLocalBytesMax, EnvLocalBytesMax, log),
		SpoolRoot:      defaultSpoolRoot(getenv(EnvSpoolRoot)),
		regionExplicit: getenv(EnvRegion) != "",
	}
	if cfg.KeyID != "" && cfg.Secret == "" {
		return cfg, fmt.Errorf("logarchive: %s set but %s empty (refusing to boot)", EnvKeyID, EnvSecret)
	}
	if cfg.KeyID == "" && cfg.Secret != "" {
		return cfg, fmt.Errorf("logarchive: %s set but %s empty (refusing to boot)", EnvSecret, EnvKeyID)
	}
	if cfg.RetentionDays < 0 {
		return cfg, fmt.Errorf("logarchive: %s = %d must be >= 0", EnvRetentionDays, cfg.RetentionDays)
	}
	if cfg.LocalBytesMax < 0 {
		return cfg, fmt.Errorf("logarchive: %s = %d must be >= 0", EnvLocalBytesMax, cfg.LocalBytesMax)
	}
	return cfg, nil
}

func defaultRegion(s string) string {
	if s == "" {
		return "us-east-1"
	}
	return s
}

func defaultSpoolRoot(s string) string {
	if s == "" {
		return DefaultSpoolRoot
	}
	return s
}

func parseDurationEnv(s string, fallback time.Duration, envName string, log *slog.Logger) time.Duration {
	if s == "" {
		return fallback
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		log.Warn("logarchive.config.parse_duration_failed",
			"env", envName, "value", s, "fallback", fallback.String(), "err", err)
		return fallback
	}
	if d <= 0 {
		log.Warn("logarchive.config.parse_duration_nonpositive",
			"env", envName, "value", s, "fallback", fallback.String())
		return fallback
	}
	return d
}

func parseIntEnv(s string, fallback int, envName string, log *slog.Logger) int {
	if s == "" {
		return fallback
	}
	var v int
	if _, err := fmt.Sscanf(s, "%d", &v); err != nil || v < 0 {
		log.Warn("logarchive.config.parse_int_failed",
			"env", envName, "value", s, "fallback", fallback, "err", err)
		return fallback
	}
	return v
}

func parseInt64Env(s string, fallback int64, envName string, log *slog.Logger) int64 {
	if s == "" {
		return fallback
	}
	var v int64
	if _, err := fmt.Sscanf(s, "%d", &v); err != nil || v < 0 {
		log.Warn("logarchive.config.parse_int64_failed",
			"env", envName, "value", s, "fallback", fallback, "err", err)
		return fallback
	}
	return v
}
