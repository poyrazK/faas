package api

// matchEdgeRuleHostLike mirrors SQL's LIKE after translating '*' to '%' and
// '?' to '_'. In particular, existing '%'/'_' selectors remain wildcards and
// backslash escapes the translated next rune. Hostnames are small; the bounded
// dynamic program avoids regexp construction and wildcard backtracking.
func matchEdgeRuleHostLike(pattern, host string) bool {
	const star, anyRune = -1, -2
	tokens := []rune(pattern)
	characters := []rune(host)
	matched := make([]bool, len(characters)+1)
	matched[0] = true
	for i := 0; i < len(tokens); i++ {
		token := tokens[i]
		switch token {
		case '*', '%':
			token = star
		case '?', '_':
			token = anyRune
		case '\\':
			i++
			if i == len(tokens) {
				return false
			}
			token = tokens[i]
			if token == '*' {
				token = '%'
			}
			if token == '?' {
				token = '_'
			}
		}
		if token == star {
			for j := 1; j < len(matched); j++ {
				matched[j] = matched[j] || matched[j-1]
			}
			continue
		}
		for j := len(characters); j > 0; j-- {
			matched[j] = matched[j-1] && (token == anyRune || token == characters[j-1])
		}
		matched[0] = false
	}
	return matched[len(characters)]
}
