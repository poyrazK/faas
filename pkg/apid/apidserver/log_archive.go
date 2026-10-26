package apidserver

import (
	"context"
	"errors"
	"log/slog"
	"os"

	"github.com/onebox-faas/faas/pkg/apid/logerasure"
	"github.com/onebox-faas/faas/pkg/logarchive"
	"github.com/onebox-faas/faas/pkg/state"
)

// openLogArchive reads the runtime-log archive configuration and the
// credentials envelope unsealed by `gregale backup unseal-archive-creds`
// (issue #562). It returns a nil client when the archive is off or cannot be
// reached; on a host that hasn't been unsealed yet the optional credential is
// absent and the archive stays off.
func openLogArchive(log *slog.Logger) (logarchive.Config, *logarchive.S3Client) {
	cfg, err := logarchive.ConfigFromEnv(os.Getenv, log)
	if err != nil {
		log.Warn("logarchive.config_failed", "err", err)
		return cfg, nil
	}
	credsPath := envOr(logarchive.EnvCredentialsPath, logarchive.DefaultCredentialsPath)
	if creds, err := logarchive.ReadCredentials(credsPath); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			log.Warn("logarchive.creds_unavailable", "path", credsPath, "err", err)
		}
	} else {
		cfg = cfg.WithCredentials(creds)
	}
	if !cfg.Enabled() {
		log.Info("logarchive.disabled", "reason", "FAAS_LOG_ARCHIVE_BUCKET unset")
		return cfg, nil
	}
	client, err := logarchive.NewS3Client(cfg.Endpoint, cfg.Region, cfg.Bucket, cfg.KeyID, cfg.Secret, cfg.AuthMode)
	if err != nil {
		log.Warn("logarchive.s3client_init_failed", "err", err)
		return cfg, nil
	}
	return cfg, client
}

// logArchiveErasure is the grace sweep's archived-log step: it deletes the
// archived runtime logs of an account before the account row goes (DPA §11).
// It does nothing on a store without erasure tracking or an apid without an
// archive bucket.
func (s *server) logArchiveErasure() logerasure.AccountErasure {
	erasure := logerasure.AccountErasure{Objects: s.logArchive, Audit: s.audit.Emit, Log: s.log}
	if store, ok := s.store.(state.LogArchiveErasureStore); ok {
		erasure.Store = store
	}
	return erasure
}

// startLogArchiveShipper runs apid's own log archive shipper: it walks the
// local spool every cfg.FlushInterval (5 min default) and gzip-PUTs .partial
// files to s3://{bucket}/faas-logs/{instance}/{YYYY}/{MM}/{DD}.jsonl.gz.
func startLogArchiveShipper(ctx context.Context, log *slog.Logger, srv *server, cfg logarchive.Config, client *logarchive.S3Client) {
	if client == nil {
		return
	}
	spool := logarchive.NewSpool(cfg.SpoolRoot, cfg.LocalBytesMax)
	shipper, err := logarchive.NewShipper(cfg, spool, client, log, logarchive.NewMetrics(srv.ops.Registry()))
	if err != nil {
		log.Warn("logarchive.shipper_init_failed", "err", err)
		return
	}
	go func() {
		if err := shipper.Run(ctx); err != nil && ctx.Err() == nil {
			log.Warn("logarchive.run_returned_error", "err", err)
		}
		_ = shipper.Spool().CloseAll()
	}()
}
