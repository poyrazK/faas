package main

import "testing"

func TestConfiguredBuildPublisherRequiresExplicitPair(t *testing.T) {
	for _, name := range []string{"none", "name", "key", "invalid"} {
		t.Run(name, func(t *testing.T) {
			values := map[string]string{}
			if name == "name" || name == "invalid" {
				values["FAAS_BUILD_PUBLISHER_NAME"] = "company"
			}
			if name == "key" || name == "invalid" {
				values["FAAS_BUILD_PUBLISHER_KEY"] = "/missing/publisher.key"
			}
			signer, err := configuredBuildPublisher(func(key string) string { return values[key] })
			if name == "none" {
				if err != nil || signer != nil {
					t.Fatal("default platform publisher selected", err)
				}
				return
			}
			if err == nil || signer != nil {
				t.Fatal("partial or invalid publisher configured")
			}
		})
	}
}
