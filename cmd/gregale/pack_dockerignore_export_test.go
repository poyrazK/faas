package main

// PackExcludes reports whether packing srcDir excludes rel.
func PackExcludes(srcDir string, dockerfile bool, rel string, isDir bool) bool {
	return loadPackRules(srcDir, dockerfile).exclude(rel, isDir)
}
