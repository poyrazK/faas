package main

import (
	"strings"
	"testing"
)

// ADR-20261110210000 §2: with SEAL_DOMAIN_KEY_SOURCE=escrow, CD reads the
// fleet and seal-domain private identities from the control plane's key
// escrow, so GitHub can hold only public recipients. These pins keep every
// staging site on that path.
func TestCDWorkflowsReadPrivateIdentitiesFromKeyEscrow(t *testing.T) {
	compute := readWorkflow(t, "cd-compute.yml")
	for _, required := range []string{
		"SEAL_DOMAIN_KEY_SOURCE: ${{ vars.SEAL_DOMAIN_KEY_SOURCE }}",
		`secrets) required_secrets+=(FLEET_AGE_KEY) ;;`,
		`mapfile -t escrow_names < <(scripts/ci/seal_domain_vars.sh --identities compute)`,
		`scripts/ci/key_escrow.sh fetch "$escrow_dir" fleet "${escrow_names[@]}"`,
		`install -m 0600 "$escrow_dir/fleet.age" "$ARTIFACT_DIR/fleet.age"`,
		`SEAL_DOMAIN_ESCROW_DIR="$escrow_dir" \`,
		`<<<"$COMPUTE_KNOWN_HOSTS"`,
		"predates the key escrow",
	} {
		if !strings.Contains(compute, required) {
			t.Errorf("cd-compute.yml key escrow wiring is missing %q", required)
		}
	}
	// The fleet identity reaches the join artifacts only from the escrow
	// or, in secrets mode, from the secret; never unconditionally.
	if strings.Count(compute, `printf '%s' "$FLEET_AGE_KEY" > "$ARTIFACT_DIR/fleet.age"`) != 1 {
		t.Error("cd-compute.yml must write FLEET_AGE_KEY to the join artifacts only in secrets mode")
	}

	controlPlane := readWorkflow(t, "cd-controlplane.yml")
	if got := strings.Count(controlPlane, "SEAL_DOMAIN_KEY_SOURCE: ${{ vars.SEAL_DOMAIN_KEY_SOURCE }}"); got != 2 {
		t.Errorf("cd-controlplane.yml passes SEAL_DOMAIN_KEY_SOURCE to %d jobs, want the deploy and host-config jobs", got)
	}
	stagings := strings.Count(controlPlane, `scripts/ci/seal_domain_vars.sh control-plane "$work/seal-domains" "$work/vars.yml"`)
	escrowed := strings.Count(controlPlane, `SEAL_DOMAIN_ESCROW_DIR="$escrow_dir" \`)
	if stagings == 0 || stagings != escrowed {
		t.Errorf("cd-controlplane.yml stages seal domains %d times, %d of them with the escrow directory", stagings, escrowed)
	}
	for _, required := range []string{
		`scripts/ci/key_escrow.sh fetch "$stage/escrow" fleet`,
		"SEAL_DOMAIN_KEY_SOURCE=escrow, but FLEET_AGE_KEY is still a GitHub secret",
	} {
		if !strings.Contains(controlPlane, required) {
			t.Errorf("cd-controlplane.yml key escrow wiring is missing %q", required)
		}
	}

	escrow := readWorkflow(t, "seal-domain-key-escrow.yml")
	for _, required := range []string{
		"github.ref == 'refs/heads/main'",
		"persist-credentials: false",
		// Private identities reach the job only in put mode.
		"FLEET_AGE_KEY: ${{ inputs.mode == 'put' && secrets.FLEET_AGE_KEY || '' }}",
		"SEAL_DOMAIN_RUNTIME_AGE_KEY: ${{ inputs.mode == 'put' && secrets.SEAL_DOMAIN_RUNTIME_AGE_KEY || '' }}",
	} {
		if !strings.Contains(escrow, required) {
			t.Errorf("seal-domain-key-escrow.yml is missing %q", required)
		}
	}
	if strings.Contains(escrow, "${{ secrets.SEAL_DOMAIN_") && strings.Contains(escrow, "_AGE_KEY }}") {
		t.Error("seal-domain-key-escrow.yml must not hand a private identity to every mode")
	}
}
