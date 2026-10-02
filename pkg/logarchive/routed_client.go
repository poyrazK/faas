package logarchive

import (
	"context"
	"errors"
	"fmt"
	"github.com/onebox-faas/faas/pkg/region"
	"github.com/onebox-faas/faas/pkg/storage"
	"io"
	"net/http"
)

// RoutedClient uses the established S3 bucket only for empty placement. Regional
// history shares the app artifact routes, including their guard outside caches.
type RoutedClient struct {
	Legacy   *S3Client
	Regional storage.StorageBackend
}

func (c *RoutedClient) route(key string) (string, error) {
	if c == nil {
		return "", errors.New("logarchive: archive store is unavailable")
	}
	id, _, _, err := region.ParseRuntimeLogArchiveKey(key)
	if err != nil {
		return "", fmt.Errorf("logarchive: %w", err)
	}
	if id != "" {
		if c.Regional == nil {
			return "", storage.ErrRegionNotConfigured
		}
		if err := storage.ValidateArtifactRoute(c.Regional, key); err != nil {
			return "", err
		}
	} else if c.Legacy == nil {
		return "", errors.New("logarchive: legacy archive store is unavailable")
	}
	return id, nil
}
func (c *RoutedClient) PutObject(ctx context.Context, key, contentType string, r io.Reader, size int64) error {
	id, err := c.route(key)
	if err != nil {
		return err
	}
	if id == "" {
		return c.Legacy.PutObject(ctx, key, contentType, r, size)
	}
	return storage.PutUncached(ctx, c.Regional, key, r)
}
func (c *RoutedClient) OpenObject(ctx context.Context, key string) (io.ReadCloser, error) {
	id, err := c.route(key)
	if err != nil {
		return nil, err
	}
	if id == "" {
		return c.Legacy.OpenObject(ctx, key)
	}
	return storage.GetUncached(ctx, c.Regional, key)
}
func (c *RoutedClient) GetObject(ctx context.Context, key string, w io.Writer) (int64, error) {
	r, err := c.OpenObject(ctx, key)
	if err != nil {
		return 0, err
	}
	defer func() { _ = r.Close() }()
	return io.Copy(w, r)
}
func (c *RoutedClient) Delete(ctx context.Context, key string) error {
	id, err := c.route(key)
	if err != nil {
		return err
	}
	if id != "" {
		return c.Regional.Delete(ctx, key)
	}
	err = c.Legacy.DeleteObject(ctx, key)
	var permanent *Permanent
	if errors.As(err, &permanent) && permanent.StatusCode == http.StatusNotFound && permanent.Code == "NoSuchKey" {
		return storage.ErrNotFound
	}
	return err
}

// ClientForConfig permits regional archives without global bucket credentials.
func ClientForConfig(cfg Config, backend storage.StorageBackend) (*RoutedClient, error) {
	c := &RoutedClient{Regional: backend}
	if cfg.Bucket != "" {
		var err error
		c.Legacy, err = NewS3Client(cfg.Endpoint, cfg.Region, cfg.Bucket, cfg.KeyID, cfg.Secret, cfg.AuthMode)
		if err != nil {
			return nil, err
		}
	}
	if cfg.Regional && backend == nil {
		return nil, storage.ErrRegionNotConfigured
	}
	return c, nil
}
