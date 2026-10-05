package objectstorage

import (
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 564
func TestBucketObjectLockRequestExactJSON(t *testing.T) {
	for _, body := range []string{
		`{"configuration":{"enabled":true}}`,
		`{"configuration":{"enabled":true,"default_retention":{"mode":"GOVERNANCE","days":3}}}`,
		`{"configuration":{"enabled":true,"default_retention":{"mode":"COMPLIANCE","years":2,"default_event_hold":{"days":5}}}}`,
		`{"configuration":{"enabled":true,"default_retention":{"mode":"GOVERNANCE","default_event_hold":{"years":1}}}}`,
	} {
		c, err := DecodeObjectBucketObjectLockRequest([]byte(body))
		if err != nil || !c.Enabled || !c.Valid() {
			t.Fatal(body, c, err)
		}
	}
	for _, body := range []string{
		``, `{}`, `null`, `[]`,
		`{"Configuration":{"enabled":true}}`,
		`{"configuration":{"enabled":true,"Enabled":false}}`,
		`{"configuration":{"enabled":true},"configuration":{"enabled":true}}`,
		`{"configuration":{"enabled":true,"default_retention":null}}`,
		`{"configuration":{"enabled":false}}`,
		`{"configuration":{"enabled":true,"future":true}}`,
		`{"configuration":{"enabled":true,"default_retention":{}}}`,
		`{"configuration":{"enabled":true,"default_retention":{"mode":"GOVERNANCE","days":3,"years":1}}}`,
		`{"configuration":{"enabled":true,"default_retention":{"mode":"COMPLIANCE","days":0}}}`,
		`{"configuration":{"enabled":true,"default_retention":{"mode":"COMPLIANCE","years":101}}}`,
		`{"configuration":{"enabled":true,"default_retention":{"mode":"COMPLIANCE","days":1,"default_event_hold":null}}}`,
		`{"configuration":{"enabled":true,"default_retention":{"mode":"COMPLIANCE","default_event_hold":{"days":1,"years":1}}}}`,
		`{"configuration":{"enabled":true}} {}`,
		strings.Repeat(" ", int(api.MaxObjectLockBodyBytes)) + `{"configuration":{"enabled":true}}`,
	} {
		c, err := DecodeObjectBucketObjectLockRequest([]byte(body))
		if !errors.Is(err, ErrInvalid) || c.Enabled || c.DefaultRetention != nil {
			t.Fatal(body, c, err)
		}
	}
}

func TestBucketObjectLockRequestXML(t *testing.T) {
	for _, body := range []string{
		`<ObjectLockConfiguration><ObjectLockEnabled>Enabled</ObjectLockEnabled></ObjectLockConfiguration>`,
		`<ObjectLockConfiguration><ObjectLockEnabled>Enabled</ObjectLockEnabled><Rule><DefaultRetention><Mode>COMPLIANCE</Mode><DefaultEventHold><Days>3</Days></DefaultEventHold></DefaultRetention></Rule></ObjectLockConfiguration>`,
	} {
		if c, err := DecodeBucketObjectLockConfiguration([]byte(body)); err != nil || !c.Enabled {
			t.Fatal(c, err)
		}
	}
	for _, body := range []string{
		`<ObjectLockConfiguration/>`,
		`<ObjectLockConfiguration><ObjectLockEnabled>Enabled</ObjectLockEnabled><Rule/></ObjectLockConfiguration>`,
		`<ObjectLockConfiguration><ObjectLockEnabled>Enabled</ObjectLockEnabled><Future/></ObjectLockConfiguration>`,
		`<ObjectLockConfiguration><ObjectLockEnabled>Enabled</ObjectLockEnabled><ObjectLockEnabled>Enabled</ObjectLockEnabled></ObjectLockConfiguration>`,
	} {
		if c, err := DecodeBucketObjectLockConfiguration([]byte(body)); !errors.Is(err, ErrInvalid) || c.Enabled {
			t.Fatal(c, err)
		}
	}
}

func TestBucketObjectLockCapabilityEnrollment(t *testing.T) {
	p := historyTestProvider(t, nil).(Provider)
	c := ObjectLockConfig{Enabled: true}
	if !SupportsNativeObjectLock(p) || !c.PublicCapabilities(p).BucketConfiguration || c.PublicCapabilities(p).DefaultEventHold || c.PublicCapabilities(p).VersionEventHold || c.PublicCapabilities(p).WriteEventHold {
		t.Fatal("wrong bucket capability")
	}
	one := int32(1)
	v := api.ObjectBucketObjectLockConfiguration{Enabled: true, DefaultRetention: &api.ObjectLockDefaultRetention{Mode: "GOVERNANCE", DefaultEventHold: &api.ObjectRetentionPeriod{Days: &one}}}
	if !errors.Is(c.ValidateConfiguration(p, v), ErrUnsupported) {
		t.Fatal("unenrolled event hold accepted")
	}
	c.EventHolds = true
	if !c.PublicCapabilities(p).DefaultEventHold || !c.PublicCapabilities(p).VersionEventHold || !c.PublicCapabilities(p).WriteEventHold {
		t.Fatal("event enrollment was not advertised")
	}
	if err := c.ValidateConfiguration(p, v); err != nil {
		t.Fatal(err)
	}
	if !errors.Is((ObjectLockConfig{}).ValidateConfiguration(p, v), ErrUnsupported) {
		t.Fatal("disabled enrollment accepted")
	}
	if (ObjectLockConfig{Enabled: true}).PublicCapabilities(struct{ Provider }{p}).BucketConfiguration {
		t.Fatal("partial provider advertised")
	}
	readsOnly := struct {
		Provider
		BucketObjectLockProvider
		ObjectVersionLockProvider
		BucketVersioningProvider
		ObjectVersionInventoryProvider
		ObjectVersionLister
	}{p, p.(BucketObjectLockProvider), p.(ObjectVersionLockProvider), p.(BucketVersioningProvider), p.(ObjectVersionInventoryProvider), p.(ObjectVersionLister)}
	if c.PublicCapabilities(readsOnly).BucketConfiguration {
		t.Fatal("provider without protected writes and lifecycle deletion advertised enrollment")
	}
	for _, tc := range []struct {
		c      ObjectLockConfig
		driver string
	}{{ObjectLockConfig{EventHolds: true}, "s3"}, {ObjectLockConfig{Enabled: true}, "gcs"}} {
		if validateObjectLockConfig(tc.c, tc.driver) == nil {
			t.Fatal("invalid enrollment accepted")
		}
	}
}

func TestBucketObjectLockRegistryContract(t *testing.T) {
	b := testBackend()
	placement := fingerprint(b)
	b.ObjectLock = ObjectLockConfig{Enabled: true, EventHolds: true}
	if fingerprint(b) != placement {
		t.Fatal("capability changed immutable placement")
	}
	c := Config{DefaultRegion: b.Region, Defaults: map[string]string{b.Region: b.ID}, Backends: []BackendConfig{b}}
	p := historyTestProvider(t, nil).(Provider)
	if _, err := NewRegistry(c, testCredentials, map[string]Factory{"s3": func(BackendConfig, func(string) string) (Provider, error) { return struct{ Provider }{p}, nil }}); err == nil {
		t.Fatal("partial native contract enrolled")
	}
	r, err := NewRegistry(c, testCredentials, map[string]Factory{"s3": func(BackendConfig, func(string) string) (Provider, error) { return p, nil }})
	if err != nil {
		t.Fatal(err)
	}
	backend, err := r.Resolve(b.ID, placement)
	if err != nil || !backend.ObjectLock.PublicCapabilities(backend.Provider).DefaultEventHold {
		t.Fatal(backend, err)
	}
}
