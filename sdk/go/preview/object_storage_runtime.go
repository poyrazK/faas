package preview

import (
	"context"
	"errors"
	"net/url"
	"regexp"
	"strings"
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

// ObjectStorageBinding owns the current client/bucket pair. Acquire leases
// through stream closure when opting into SDK-owned retirement.
type ObjectStorageBinding[T any] struct{ *clientBinding[T] }

func NewObjectStorageBinding[T any](prefix string, factory func(context.Context, ObjectStorageClientConfig) (T, error), options ...ObjectStorageBindingOptions[T]) (*ObjectStorageBinding[T], error) {
	if !objectStoragePrefix.MatchString(prefix) || factory == nil || len(options) > 1 {
		return nil, errors.New("faas: invalid object-storage binding")
	}
	core := &clientBinding[T]{label: "object-storage", resource: "client", reloads: make(chan struct{}, 1), retired: make(map[*bindingClient[T]]struct{}), changed: make(chan struct{})}
	core.prepare = func(values map[string]string) (func(context.Context) (T, string, error), error) {
		config, err := loadObjectStorageConfig(prefix, values)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context) (T, string, error) {
			client, err := factory(ctx, config)
			return client, config.Bucket, err
		}, nil
	}
	if len(options) == 1 {
		core.retire, core.onRetireError = options[0].Retire, options[0].OnRetireError
	}
	return &ObjectStorageBinding[T]{core}, nil
}

func (b *ObjectStorageBinding[T]) Current() (T, string, error) { return b.current() }
func (b *ObjectStorageBinding[T]) Acquire() (*ObjectStorageLease[T], error) {
	lease, err := b.acquire()
	if err != nil {
		return nil, err
	}
	return &ObjectStorageLease[T]{lease}, nil
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
