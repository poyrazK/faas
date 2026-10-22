//go:build linux

package promotionrollout

import (
	"github.com/jackc/pgx/v5"
	"net"
	"os"
	"runtime"
	"strings"
	"syscall"
)

// This exception is only for the existing guarded native harness: a root-owned
// dedicated host, KVM and an isolated local test database are all mandatory.
// The caller flag alone never opens production admission.
func nativeQualificationHost(dsn string) bool {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" || os.Geteuid() != 0 {
		return false
	}
	marker, err := os.Lstat("/etc/faas/builder-acceptance-host")
	if err != nil || !marker.Mode().IsRegular() || marker.Mode().Perm()&0022 != 0 {
		return false
	}
	if stat, ok := marker.Sys().(*syscall.Stat_t); !ok || stat.Uid != 0 {
		return false
	}
	kvm, err := os.Stat("/dev/kvm")
	if err != nil || kvm.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	config, err := pgx.ParseConfig(dsn)
	if err != nil || !strings.HasPrefix(config.Database, "faas_test_") || strings.HasPrefix(config.Database, "faas_test_template_") {
		return false
	}
	if strings.HasPrefix(config.Host, "/") {
		return true
	}
	ip := net.ParseIP(config.Host)
	return config.Host == "localhost" || ip != nil && ip.IsLoopback()
}
