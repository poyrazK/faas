package main

import "runtime"

// runtimeVersion is captured at build time so the response stays
// self-describing. Kept in a separate file so the import is only
// required once.
var runtimeVersion = runtime.Version()
