// Package dataapiinspectioncommand defines the reserved, bounded platform
// inspection command independently of its guest implementation.
package dataapiinspectioncommand

const Command = "__gregale_data_api_write_inspection_v1__"

func IsCommand(command []string) bool { return len(command) > 0 && command[0] == Command }

// Valid checks only the fixed table and nonce. It does not establish task
// provenance or the freshness of the nonce.
func Valid(command []string, shell bool) bool {
	if shell || len(command) != 3 || !IsCommand(command) || len(command[1]) < 1 || len(command[1]) > 63 || len(command[2]) != 64 {
		return false
	}
	for i, c := range command[1] {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && c != '_' && (i == 0 || c < '0' || c > '9') {
			return false
		}
	}
	for _, c := range command[2] {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
