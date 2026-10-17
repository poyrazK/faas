package api

import "regexp"

// Mirrors the server's public queue binding name grammar.
var queueBindingNameRE = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

func ValidQueueBindingName(value string) bool { return queueBindingNameRE.MatchString(value) }
