package dbvm

import (
	"slices"
	"testing"
)

func TestBaseExtensionDirectory(t *testing.T) {
	list := "/12/040755/0/0/.//\n/2/040755/0/0/..//\n/13/100644/0/0/pg_trgm.control/183/\n/14/100644/0/0/pg_trgm--1.5--1.6.sql/679/\n"
	if files := extensionDirectory(list); !slices.Equal(files, []string{"pg_trgm.control", "pg_trgm--1.5--1.6.sql"}) {
		t.Fatalf("extension files = %v", files)
	}
}

func TestBaseExtensionDirectoryRecognizesVectorLibrary(t *testing.T) {
	list := "/13/100755/0/0/vector.so/82368/\n/14/100755/0/0/other.so/8192/\n"
	if files := extensionDirectory(list); !slices.Equal(files, []string{"vector.so"}) {
		t.Fatalf("shared library inventory = %v", files)
	}
}
