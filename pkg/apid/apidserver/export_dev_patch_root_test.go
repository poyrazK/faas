package apidserver

// ArchiveLogicalRoot exposes the root a developer live patch is planned
// against: the selected root below the CLI's single wrapper directory.
func ArchiveLogicalRoot(path, sourceRoot string) (string, error) {
	return archiveLogicalRoot(path, sourceRoot)
}
