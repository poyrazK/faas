package edgewaf

import (
	"io/fs"
	"strings"
)

// rxRenamer turns CRS's @rx into @grx (rxop.go), in both the plain and the
// negated form, which are the only two CRS writes. Rules that leave the
// operator out (three Content-Type checks in coraza.conf-recommended) keep
// Coraza's default @rx.
var rxRenamer = strings.NewReplacer(`"@rx `, `"@`+rxOperatorName+` `, `"!@rx `, `"!@`+rxOperatorName+` `)

// rxRenameFS serves an FS's rule files with @rx renamed. Directory reads,
// used by Include globs, go to the FS unchanged; Coraza reads each file
// with fs.ReadFile, which prefers ReadFile.
type rxRenameFS struct{ fs.FS }

func (r rxRenameFS) ReadFile(name string) ([]byte, error) {
	b, err := fs.ReadFile(r.FS, name)
	if err != nil || !strings.Contains(name, ".conf") {
		return b, err
	}
	return []byte(rxRenamer.Replace(string(b))), nil
}
