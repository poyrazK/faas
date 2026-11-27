package main

// adr: 20261117190000 — secretd signs per-object shared-store grants.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	gcs "cloud.google.com/go/storage"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/onebox-faas/faas/pkg/secretd"
	"github.com/onebox-faas/faas/pkg/storagegrant"
)

// storageGrantsConfig is FAAS_SECRETD_STORAGE_*: which store secretd signs
// for. Off by default: GrantStorageObjects refuses.
type storageGrantsConfig struct {
	backend  string // off, gcs or s3
	bucket   string
	endpoint string
	region   string
	key      string
	secret   string
}

func storageGrantsFromEnv(getenv func(string) string) (storageGrantsConfig, error) {
	c := storageGrantsConfig{
		backend:  getenv("FAAS_SECRETD_STORAGE_GRANTS"),
		bucket:   getenv("FAAS_SECRETD_STORAGE_BUCKET"),
		endpoint: getenv("FAAS_SECRETD_STORAGE_S3_ENDPOINT"),
		region:   getenv("FAAS_SECRETD_STORAGE_S3_REGION"),
		key:      getenv("FAAS_SECRETD_STORAGE_S3_ACCESS_KEY"),
	}
	switch c.backend {
	case "", "off":
		c.backend = "off"
		return c, nil
	case "gcs":
		if c.bucket == "" {
			return c, errors.New("secretd: FAAS_SECRETD_STORAGE_GRANTS=gcs needs FAAS_SECRETD_STORAGE_BUCKET")
		}
		return c, nil
	case "s3":
		if path := getenv("FAAS_SECRETD_STORAGE_S3_SECRET_KEY_FILE"); path != "" {
			raw, err := os.ReadFile(path) //nolint:gosec // an operator-set systemd credential path
			if err != nil {
				return c, fmt.Errorf("secretd: FAAS_SECRETD_STORAGE_S3_SECRET_KEY_FILE: %w", err)
			}
			c.secret = strings.TrimSpace(string(raw))
		}
		if c.bucket == "" || c.endpoint == "" || c.region == "" || c.key == "" || c.secret == "" {
			return c, errors.New("secretd: FAAS_SECRETD_STORAGE_GRANTS=s3 needs FAAS_SECRETD_STORAGE_BUCKET, FAAS_SECRETD_STORAGE_S3_{ENDPOINT,REGION,ACCESS_KEY} and FAAS_SECRETD_STORAGE_S3_SECRET_KEY_FILE")
		}
		return c, nil
	}
	return c, fmt.Errorf("secretd: FAAS_SECRETD_STORAGE_GRANTS=%q: want off, gcs or s3", c.backend)
}

// signer builds the configured signer; nil when off.
func (c storageGrantsConfig) signer(ctx context.Context) (storagegrant.Signer, func(), error) {
	switch c.backend {
	case "gcs":
		client, err := gcs.NewClient(ctx, gcs.WithDisabledClientMetrics())
		if err != nil {
			return nil, nil, fmt.Errorf("secretd: storage grants: GCS client: %w", err)
		}
		s, err := storagegrant.NewGCSSigner(client, c.bucket)
		if err != nil {
			_ = client.Close()
			return nil, nil, err
		}
		return s, func() { _ = client.Close() }, nil
	case "s3":
		client := s3.New(s3.Options{
			Region:                     c.region,
			BaseEndpoint:               aws.String(c.endpoint),
			UsePathStyle:               true,
			Credentials:                credentials.NewStaticCredentialsProvider(c.key, c.secret, ""),
			RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired,
			ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired,
		})
		s, err := storagegrant.NewS3Signer(client, c.bucket)
		if err != nil {
			return nil, nil, err
		}
		return s, func() {}, nil
	}
	return nil, func() {}, nil
}

// storageGrantStore is store when grants are on, nil otherwise, so
// WithStorageGrants stays off without a signer.
func storageGrantStore(store secretd.StorageGrantStore, signer storagegrant.Signer) secretd.StorageGrantStore {
	if signer == nil {
		return nil
	}
	return store
}
