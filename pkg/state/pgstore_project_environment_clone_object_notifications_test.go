//go:build !no_pg

// adr: 590
package state_test

import "testing"

func TestPgCloneCapturesBucketNotifications(t *testing.T) {
	s, _, _ := pgWithPool(t)
	cloneBucketNotificationCapture(t, s)
}
