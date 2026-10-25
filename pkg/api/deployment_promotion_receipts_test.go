// adr: 20261025090000
package api

import (
	"github.com/google/uuid"
	"testing"
)

func TestDeploymentPromotionRequestHashCanonicalIDs(t *testing.T) {
	candidate := "0123456789abcdef0123456789abcdef"
	serving := "fedcba9876543210fedcba9876543210"
	canonical := uuid.MustParse(serving).String()
	req := UpdateDeploymentTrafficRequest{TrafficPercent: 100, ExpectedServingDeploymentID: &serving}
	original, err := DeploymentPromotionRequestHash(candidate, req)
	if err != nil {
		t.Fatal(err)
	}
	req.ExpectedServingDeploymentID = &canonical
	normalized, err := DeploymentPromotionRequestHash(uuid.MustParse(candidate).String(), req)
	if err != nil || original != normalized {
		t.Fatal("equivalent native IDs changed the receipt fingerprint")
	}
	other := uuid.NewString()
	req.ExpectedServingDeploymentID = &other
	changed, err := DeploymentPromotionRequestHash(candidate, req)
	if err != nil || changed == original {
		t.Fatal("different original serving guard reused the fingerprint")
	}
	req.TrafficPercent = 50
	different, err := DeploymentPromotionRequestHash(candidate, req)
	if err != nil || different == changed {
		t.Fatal("different request body reused the fingerprint")
	}
}
