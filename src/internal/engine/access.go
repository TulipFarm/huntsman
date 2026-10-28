package engine

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
)

// Key is a named API key. Request logs carry the name, never the token.
type Key struct{ Name, Token string }

var keyName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

// ParseKeys reads HUNTSMAN_TOKEN (one key, named "default") or HUNTSMAN_TOKENS ("name:token,..."). Setting both is an
// error so a forgotten single token cannot outlive a key rotation.
func ParseKeys(single, named string) ([]Key, error) {
	single, named = strings.TrimSpace(single), strings.TrimSpace(named)
	if single != "" && named != "" {
		return nil, errors.New("set HUNTSMAN_TOKEN or HUNTSMAN_TOKENS, not both")
	}
	if single != "" {
		return []Key{{"default", single}}, nil
	}
	var keys []Key
	names, tokens := map[string]bool{}, map[string]bool{}
	for _, entry := range strings.Split(named, ",") {
		if entry = strings.TrimSpace(entry); entry == "" {
			continue
		}
		name, token, ok := strings.Cut(entry, ":")
		if !ok || !keyName.MatchString(name) || token == "" {
			return nil, errors.New("HUNTSMAN_TOKENS entries must be name:token, with names of lowercase letters, digits, - or _")
		}
		if names[name] || tokens[token] {
			return nil, fmt.Errorf("HUNTSMAN_TOKENS repeats key %q or its token", name)
		}
		names[name], tokens[token] = true, true
		keys = append(keys, Key{name, token})
	}
	return keys, nil
}

// authenticate returns the name of the key in the Authorization header. It compares against every key so the time
// taken does not reveal which one matched.
func (s *Service) authenticate(header string) (string, bool) {
	if len(s.Keys) == 0 {
		return "", true
	}
	name := ""
	for _, k := range s.Keys {
		if subtle.ConstantTimeCompare([]byte(header), []byte("Bearer "+k.Token)) == 1 {
			name = k.Name
		}
	}
	return name, name != ""
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
