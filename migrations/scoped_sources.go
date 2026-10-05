package migrations

// adr: 590. Adapt issued public declarations to isolated migration targets.

import (
	"bytes"
	"io/fs"
	"regexp"

	"github.com/jackc/pgx/v5"
)

// SourcesForSchema binds the five issued public declarations to the target
// schema. Public execution and every other embedded source retain exact bytes.
func SourcesForSchema(schema string) fs.FS {
	if schema == "public" {
		return FS
	}
	return scopedMigrationFS{FS: FS, schema: pgx.Identifier{schema}.Sanitize()}
}

type scopedMigrationFS struct {
	fs.FS
	schema string
}

func (s scopedMigrationFS) Open(name string) (fs.File, error) {
	if !issuedPublicDeclarationMigration(name) {
		return s.FS.Open(name)
	}
	body, err := fs.ReadFile(s.FS, name)
	if err != nil {
		return nil, err
	}
	info, err := fs.Stat(s.FS, name)
	if err != nil {
		return nil, err
	}
	body = scopeIssuedMigrationDeclarations(body, s.schema)
	return &scopedMigrationFile{Reader: bytes.NewReader(body), info: scopedMigrationInfo{FileInfo: info, size: int64(len(body))}}, nil
}

func issuedPublicDeclarationMigration(name string) bool {
	switch name {
	case "20261003160458628_application_standard_runtime_default_base.sql",
		"20261003210400000_application_standard_source_build_rootfs.sql",
		"20261004043519306_application_standard_exception_authority.sql",
		"20261004232943929_application_standard_retained_native_admission.sql",
		"20261004234710285_application_standard_retained_native_protocol.sql":
		return true
	default:
		return false
	}
}

var issuedPublicFunctionDeclaration = regexp.MustCompile(`(?m)^(CREATE(?: OR REPLACE)? FUNCTION )public\.(application_standard_[a-z_]+\()`)
var issuedPublicCompositeDeclaration = regexp.MustCompile(`(?m)^(?:CREATE(?: OR REPLACE)?|DROP) FUNCTION (?:source_build_rootfs_intent|application_standard_runtime_root_producer)\([^\n]*`)

func scopeIssuedMigrationDeclarations(body []byte, schema string) []byte {
	body = issuedPublicFunctionDeclaration.ReplaceAllFunc(body, func(declaration []byte) []byte {
		return bytes.Replace(declaration, []byte("public."), []byte(schema+"."), 1)
	})
	return issuedPublicCompositeDeclaration.ReplaceAllFunc(body, func(declaration []byte) []byte {
		declaration = bytes.ReplaceAll(declaration, []byte("public.apps"), []byte(schema+".apps"))
		return bytes.ReplaceAll(declaration, []byte("public.deployments"), []byte(schema+".deployments"))
	})
}

type scopedMigrationFile struct {
	*bytes.Reader
	info fs.FileInfo
}

func (f *scopedMigrationFile) Stat() (fs.FileInfo, error) { return f.info, nil }
func (*scopedMigrationFile) Close() error                 { return nil }

type scopedMigrationInfo struct {
	fs.FileInfo
	size int64
}

func (i scopedMigrationInfo) Size() int64 { return i.size }
