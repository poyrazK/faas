//go:build !no_pg

package state_test

import "testing"

func TestPgProjectEnvironmentCloneCapturesAndPreparesWorkloads(t *testing.T) {
	s, _, _ := pgWithPool(t)
	projectEnvironmentCloneCapturesAndPreparesWorkloads(t, s)
}

// ADR-375: the publication gate requires the captured project configuration.
func TestPgProjectEnvironmentCloneRequiresProjectConfigurationReceipt(t *testing.T) {
	s, _, _ := pgWithPool(t)
	projectEnvironmentCloneCapturesAndPreparesWorkloads(t, s, true)
}

// ADR-375: actual sealed target values and completeness receipts are checked.
func TestPgProjectEnvironmentCloneValuePublication(t *testing.T) {
	for _, fault := range cloneValuePublicationFaults {
		t.Run(fault, func(t *testing.T) {
			s, _, _ := pgWithPool(t)
			projectEnvironmentClonePublicationContract(t, s, false, fault)
		})
	}
}
