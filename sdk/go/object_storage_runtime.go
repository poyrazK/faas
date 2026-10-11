package faas

import (
	"context"
	"errors"
	"net/url"
	"reflect"
	"regexp"
	"strings"
	"sync"
)

var objectStoragePrefix = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,47}$`)

// ObjectStorageClientConfig is a single managed-binding snapshot. The factory
// must configure a path-style S3 client with exactly these credentials. It must
// not fall back to ambient AWS or upstream provider credentials.
type ObjectStorageClientConfig struct {
	Endpoint, Region, Bucket     string
	AccessKeyID, SecretAccessKey string `json:"-"`
	UsePathStyle                 bool
	RequiredChecksumsOnly        bool
}

func (ObjectStorageClientConfig) String() string   { return "ObjectStorageClientConfig{redacted}" }
func (ObjectStorageClientConfig) GoString() string { return "ObjectStorageClientConfig{redacted}" }

// ObjectStorageBinding owns the current client/bucket pair. Factories permit
// using the application's S3 library without adding one to this leaf SDK.
// Reacquire Current for each request; existing requests may finish on an old
// client. The application owns the lifetime of retired clients.
type ObjectStorageBinding[T any] struct {
	mu      sync.RWMutex
	prefix  string
	factory func(context.Context, ObjectStorageClientConfig) (T, error)
	client  T
	bucket  string
	ready   bool
}

func NewObjectStorageBinding[T any](prefix string, factory func(context.Context, ObjectStorageClientConfig) (T, error)) (*ObjectStorageBinding[T], error) {
	if !objectStoragePrefix.MatchString(prefix) || factory == nil {
		return nil, errors.New("faas: invalid object-storage binding")
	}
	return &ObjectStorageBinding[T]{prefix: prefix, factory: factory}, nil
}

// Reload constructs and optionally tests a candidate in the supplied factory,
// then atomically publishes it. Use the secrets map from Gregale's atomic
// {revision,secrets} envelope, not stale process environment after a rotation.
// ACK that envelope only after Reload succeeds and all other consumers apply.
// Failure disables new acquisition, including missing secrets after revocation.
// Errors deliberately omit configuration, credentials and driver diagnostics.
func (b *ObjectStorageBinding[T]) Reload(ctx context.Context, values map[string]string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.ready = false
	var zero T
	b.client, b.bucket = zero, ""
	config, err := loadObjectStorageConfig(b.prefix, values)
	if err != nil {
		return err
	}
	if ctx.Err() != nil {
		return errors.New("faas: object-storage reload cancelled")
	}
	client, err := b.factory(ctx, config)
	if err != nil {
		return errors.New("faas: object-storage client could not be configured")
	}
	v := reflect.ValueOf(client)
	if !v.IsValid() || (v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface || v.Kind() == reflect.Map || v.Kind() == reflect.Slice || v.Kind() == reflect.Func || v.Kind() == reflect.Chan) && v.IsNil() {
		return errors.New("faas: object-storage client is unavailable")
	}
	if ctx.Err() != nil {
		return errors.New("faas: object-storage reload cancelled")
	}
	b.client, b.bucket, b.ready = client, config.Bucket, true
	return nil
}

func (b *ObjectStorageBinding[T]) Current() (T, string, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if !b.ready {
		var zero T
		return zero, "", errors.New("faas: object-storage binding is not ready")
	}
	return b.client, b.bucket, nil
}

func loadObjectStorageConfig(prefix string, values map[string]string) (ObjectStorageClientConfig, error) {
	config := ObjectStorageClientConfig{Endpoint: values[prefix+"_ENDPOINT"], Region: values[prefix+"_REGION"], Bucket: values[prefix+"_BUCKET"],
		AccessKeyID: values[prefix+"_ACCESS_KEY_ID"], SecretAccessKey: values[prefix+"_SECRET_ACCESS_KEY"], UsePathStyle: true, RequiredChecksumsOnly: true}
	for _, value := range []string{config.Endpoint, config.Region, config.Bucket, config.AccessKeyID, config.SecretAccessKey, values[prefix+"_ADDRESSING_STYLE"]} {
		if value == "" || strings.ContainsAny(value, "\x00\r\n") {
			return ObjectStorageClientConfig{}, errors.New("faas: object-storage binding environment is incomplete or invalid")
		}
	}
	u, err := url.Parse(config.Endpoint)
	if err != nil || u.Hostname() == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.Fragment != "" ||
		values[prefix+"_ADDRESSING_STYLE"] != "path" || strings.ContainsAny(config.Region+config.Bucket, "/\\ \t?#") {
		return ObjectStorageClientConfig{}, errors.New("faas: object-storage binding configuration is invalid")
	}
	return config, nil
}
