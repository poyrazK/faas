// Package postgresstarter uploads the shipped customer starter unchanged. The
// only fixture file is a public CA for the disposable SQL server.
package postgresstarter

import (
	"bytes"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io/fs"
	"strings"

	"github.com/onebox-faas/faas/cmd/gregale/templates"
)

// CAPath is covered by the unchanged starter Dockerfile's runtime COPY.
const CAPath = "runtime/fixture-ca.pem"

// Starter describes an unchanged embedded app's native acceptance configuration.
type Starter struct {
	Template, Runtime, PhasePrefix, TrustEnv, ReleaseCommand string
	TaskCommand                                              []string
}

// Starters lists the supported customer SQL workloads in deployment order.
func Starters() []Starter {
	return []Starter{
		{Template: "rest-api-postgres", Runtime: "node22", PhasePrefix: "starter", TrustEnv: "NODE_EXTRA_CA_CERTS", ReleaseCommand: "node migrate.js",
			TaskCommand: []string{"node", "-e", `if (!process.env.DATABASE_URL || Object.hasOwn(process.env, 'MIGRATION_DATABASE_URL')) process.exit(1)`}},
		{Template: "rest-api-postgres-python", Runtime: "python312", PhasePrefix: "python_starter", TrustEnv: "SSL_CERT_FILE", ReleaseCommand: "python migrate.py",
			TaskCommand: []string{"python", "-c", `import os; raise SystemExit(0 if os.environ.get('DATABASE_URL') and 'MIGRATION_DATABASE_URL' not in os.environ else 1)`}},
	}
}

func Source(template string, ca []byte) (map[string]string, error) {
	supported := false
	for _, starter := range Starters() {
		supported = supported || starter.Template == template
	}
	if !supported {
		return nil, fmt.Errorf("unsupported PostgreSQL starter")
	}
	rest := bytes.TrimSpace(ca)
	certificates := 0
	for len(rest) > 0 {
		if !bytes.HasPrefix(rest, []byte("-----BEGIN CERTIFICATE-----")) {
			return nil, fmt.Errorf("starter fixture trust must contain certificates only")
		}
		block, remaining := pem.Decode(rest)
		if block == nil || block.Type != "CERTIFICATE" || len(block.Headers) != 0 {
			return nil, fmt.Errorf("starter fixture trust must contain certificates only")
		}
		if _, err := x509.ParseCertificate(block.Bytes); err != nil {
			return nil, fmt.Errorf("starter fixture certificate: %w", err)
		}
		certificates++
		rest = bytes.TrimSpace(remaining)
	}
	if certificates == 0 {
		return nil, fmt.Errorf("starter fixture CA is required")
	}
	source := make(map[string]string)
	err := fs.WalkDir(templates.FS, template, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		content, err := fs.ReadFile(templates.FS, path)
		if err != nil {
			return err
		}
		source[strings.TrimPrefix(path, template+"/")] = string(content)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read embedded PostgreSQL starter: %w", err)
	}
	source[CAPath] = string(ca)
	return source, nil
}
