package preview

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func runtimeStorageValues() map[string]string {
	return map[string]string{"ASSETS_ENDPOINT": "https://s3.gregale.test", "ASSETS_REGION": "auto", "ASSETS_BUCKET": "assets", "ASSETS_ACCESS_KEY_ID": "private-access", "ASSETS_SECRET_ACCESS_KEY": "private-secret", "ASSETS_ADDRESSING_STYLE": "path"}
}

func TestObjectStorageRuntimeSlowFactoryDoesNotBlockCurrent(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	b, err := NewObjectStorageBinding("ASSETS", func(context.Context, ObjectStorageClientConfig) (string, error) {
		close(entered)
		<-release
		return "candidate", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	reloaded := make(chan error, 1)
	go func() { reloaded <- b.Reload(context.Background(), runtimeStorageValues()) }()
	<-entered
	defer func() { close(release); <-reloaded }()
	acquired := make(chan error, 1)
	go func() { _, _, err := b.Current(); acquired <- err }()
	select {
	case err := <-acquired:
		if err == nil {
			t.Fatal("client available before candidate was ready")
		}
	case <-time.After(time.Second):
		t.Fatal("Current blocked behind the client factory")
	}
}

func TestObjectStorageRuntimeRotationRevocationAndSafeErrors(t *testing.T) {
	b, err := NewObjectStorageBinding("ASSETS", func(ctx context.Context, c ObjectStorageClientConfig) (*string, error) {
		if !c.UsePathStyle || !c.RequiredChecksumsOnly || c.Endpoint != "https://s3.gregale.test" || c.Region != "auto" || c.Bucket != "assets" {
			t.Fatal("wrong client configuration")
		}
		raw, _ := json.Marshal(c)
		if strings.Contains(string(raw)+fmt.Sprintf("%+v %#v", c, c), "private-") {
			t.Fatal("config logging exposed credentials")
		}
		if c.SecretAccessKey == "private-rejected" {
			return nil, errors.New("private-driver-error")
		}
		return &c.SecretAccessKey, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = b.Current(); err == nil {
		t.Fatal("uninitialized binding ready")
	}
	values := runtimeStorageValues()
	if err = b.Reload(context.Background(), values); err != nil {
		t.Fatal(err)
	}
	first, bucket, err := b.Current()
	if err != nil || bucket != "assets" || *first != "private-secret" {
		t.Fatal("wrong current pair")
	}
	values["ASSETS_SECRET_ACCESS_KEY"] = "private-rotated"
	if err = b.Reload(context.Background(), values); err != nil {
		t.Fatal(err)
	}
	second, _, _ := b.Current()
	if *second != "private-rotated" || *first != "private-secret" {
		t.Fatal("rotation mixed clients")
	}
	values["ASSETS_SECRET_ACCESS_KEY"] = "private-rejected"
	if err = b.Reload(context.Background(), values); err == nil || strings.Contains(err.Error(), "private-") {
		t.Fatal("unsafe factory failure")
	}
	if _, _, err = b.Current(); err == nil {
		t.Fatal("failed reload kept new acquisition enabled")
	}
	delete(values, "ASSETS_SECRET_ACCESS_KEY")
	if err = b.Reload(context.Background(), values); err == nil {
		t.Fatal("revoked credentials accepted")
	}
}

func TestObjectStorageRuntimeValidationAndConcurrency(t *testing.T) {
	for _, input := range []struct{ key, value string }{{"ASSETS_ENDPOINT", "https://user:private-secret@example.test"}, {"ASSETS_ENDPOINT", "https://example.test?secret=x"}, {"ASSETS_ADDRESSING_STYLE", "virtual"}, {"ASSETS_BUCKET", "../other"}, {"ASSETS_REGION", "us east"}} {
		values := runtimeStorageValues()
		values[input.key] = input.value
		b, _ := NewObjectStorageBinding("ASSETS", func(context.Context, ObjectStorageClientConfig) (string, error) {
			t.Fatal("invalid config reached factory")
			return "", nil
		})
		if b.Reload(context.Background(), values) == nil {
			t.Fatal("invalid config accepted")
		}
	}
	b, _ := NewObjectStorageBinding("ASSETS", func(_ context.Context, c ObjectStorageClientConfig) (string, error) { return c.SecretAccessKey, nil })
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := b.Reload(context.Background(), runtimeStorageValues()); err != nil {
				t.Error(err)
			}
			_, _, _ = b.Current()
		}()
	}
	wg.Wait()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if b.Reload(ctx, runtimeStorageValues()) == nil {
		t.Fatal("cancelled reload applied")
	}
}
