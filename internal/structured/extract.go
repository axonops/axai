package structured

import (
	"encoding/json"
	"errors"
	"strings"
)

// ErrNoJSON means the reply held no JSON object.
var ErrNoJSON = errors.New("no JSON object found in reply")

// ExtractJSON returns the first balanced, valid JSON object in s. Models often
// wrap JSON in markdown fences or add prose around it.
func ExtractJSON(s string) (string, error) {
	for start := strings.IndexByte(s, '{'); start >= 0; {
		if end := matchBrace(s[start:]); end > 0 {
			if candidate := s[start : start+end]; json.Valid([]byte(candidate)) {
				return candidate, nil
			}
		}
		next := strings.IndexByte(s[start+1:], '{')
		if next < 0 {
			break
		}
		start += 1 + next
	}
	return "", ErrNoJSON
}

// matchBrace returns the length of the brace-balanced prefix of s, which
// starts with '{', or -1. Braces inside JSON strings are ignored.
func matchBrace(s string) int {
	depth, inStr, esc := 0, false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inStr {
			switch {
			case esc:
				esc = false
			case c == '\\':
				esc = true
			case c == '"':
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i + 1
			}
		}
	}
	return -1
}
