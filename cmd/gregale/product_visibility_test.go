package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestProductControlIsHiddenFromCustomerDiscovery(t *testing.T) {
	for _, command := range customerCliCommands() {
		if command.Name == "product" {
			t.Fatal("Product Control is listed in customer commands")
		}
	}
	for _, tc := range []struct {
		name   string
		render func() int
	}{
		{"bash", cmdCompletionBash},
		{"zsh", cmdCompletionZsh},
		{"fish", cmdCompletionFish},
		{"powershell", cmdCompletionPowershell},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			captureStdoutSwap(t, &out, tc.render)
			for _, private := range []string{"Product Control", "product-control", "\"product\"", "'product'", "_gregale_product"} {
				if strings.Contains(out.String(), private) {
					t.Errorf("%s completion exposes %q", tc.name, private)
				}
			}
		})
	}
}
