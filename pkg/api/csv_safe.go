package api

import "strings"

// CSVSafeCell keeps a spreadsheet from running a cell as a formula. A value
// that starts with =, +, -, @, a tab or a carriage return is written with a
// leading apostrophe (OWASP CSV injection guidance). Every CSV export of a
// value a customer or their callers chose (route labels, end users, tags,
// names) passes through it.
func CSVSafeCell(value string) string {
	if value != "" && strings.ContainsRune("=+-@\t\r", rune(value[0])) {
		return "'" + value
	}
	return value
}
