package tcpd

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"os"
	"syscall"

	"github.com/onebox-faas/faas/pkg/api"
)

// FileCertificateProvider reads <hostname>.pem containing both certificate chain
// and key beneath an anchored root. Provision by atomic rename of the complete
// bundle; each lookup sees one generation. No certificate cache grows with apps.
type FileCertificateProvider struct{ root *os.Root }

func NewFileCertificateProvider(directory string) (*FileCertificateProvider, error) {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, errors.New("cannot open TCP TLS certificate directory")
	}
	if err := validateCertificateDirectory(root); err != nil {
		_ = root.Close()
		return nil, err
	}
	return &FileCertificateProvider{root: root}, nil
}

func validateCertificateDirectory(root *os.Root) error {
	info, err := root.Stat(".")
	if err != nil || !info.IsDir() || info.Mode().Perm()&0022 != 0 {
		return errors.New("TCP TLS certificate directory must not be writable by group or others")
	}
	return nil
}

func (p *FileCertificateProvider) Close() error {
	if p == nil || p.root == nil {
		return nil
	}
	return p.root.Close()
}

func (p *FileCertificateProvider) Certificate(ctx context.Context, hostname string) (*tls.Certificate, error) {
	if p == nil || p.root == nil || ctx == nil {
		return nil, errors.New("TCP TLS certificate provider unavailable")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Recheck the anchored directory so permission changes after startup also
	// fail closed. File permissions alone cannot prevent bundle replacement.
	if err := validateCertificateDirectory(p.root); err != nil {
		return nil, err
	}
	policy, err := (api.TCPListenerTLSConfig{Mode: api.TCPListenerTLSTerminate, Hostname: hostname}).Normalize()
	if err != nil {
		return nil, err
	}
	// Nonblocking open prevents an incorrectly provisioned FIFO from holding
	// a handshake forever before we can reject its file type.
	file, err := p.root.OpenFile(policy.Hostname+".pem", os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, errors.New("TCP TLS certificate bundle unavailable")
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > api.TCPListenerTLSBundleMaxBytes || info.Mode().Perm()&0027 != 0 {
		return nil, errors.New("TCP TLS bundle must be a bounded regular file without public access or group write permission")
	}
	bundle, err := io.ReadAll(io.LimitReader(file, api.TCPListenerTLSBundleMaxBytes+1))
	if err != nil || len(bundle) > api.TCPListenerTLSBundleMaxBytes {
		return nil, errors.New("TCP TLS certificate bundle read failed")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	certificate, err := tls.X509KeyPair(bundle, bundle)
	if err != nil {
		return nil, errors.New("TCP TLS certificate bundle invalid")
	}
	return &certificate, nil
}
