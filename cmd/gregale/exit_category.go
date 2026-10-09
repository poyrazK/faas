package main

// exitCategory describes shared command failures. Dedicated diagnostic and
// wait commands may have their own exit contracts.
func exitCategory(code int) string {
	switch code {
	case 1:
		return "invalid_request"
	case 2:
		return "authentication"
	case 3:
		return "temporary_failure"
	case 4:
		return "not_found"
	case 5:
		return "conflict"
	case 6:
		return "permission_denied"
	case 130:
		return "cancelled"
	default:
		return "command_failure"
	}
}
