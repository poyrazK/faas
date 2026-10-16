//go:build metal && promotionacceptance

package e2etest

import "testing"

func TestPromotionDaemonsIncludePublicationBarriers(t *testing.T) {
	if daemonBuildTags != "metal,promotionacceptance" {
		t.Fatal("native promotion daemons would silently omit publication barriers", daemonBuildTags)
	}
}
