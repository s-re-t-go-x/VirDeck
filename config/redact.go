package config

import (
	"errors"
	"net/url"
	"strings"
)

// RedactProxyURL masks any password in a proxy URL with literal "***" for safe logging and UI display.
func RedactProxyURL(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	u, err := url.Parse(trimmed)
	if err == nil {
		if u.User != nil {
			if _, hasPass := u.User.Password(); hasPass {
				user := u.User.Username()
				u.User = nil
				rendered := u.String()
				if idx := strings.Index(rendered, "://"); idx >= 0 {
					prefix := rendered[:idx+3]
					rest := strings.TrimPrefix(rendered[idx+3:], "@")
					return prefix + url.PathEscape(user) + ":***@" + rest
				}
			}
		}
		return trimmed
	}
	// Fallback for malformed URLs (or passwords containing unencoded '/', '?', '#') that still embed "://user:pass@host".
	if schemeIdx := strings.Index(trimmed, "://"); schemeIdx >= 0 {
		rest := trimmed[schemeIdx+3:]
		if atIdx := strings.LastIndex(rest, "@"); atIdx >= 0 {
			creds := rest[:atIdx]
			afterAt := rest[atIdx+1:]
			if colonIdx := strings.Index(creds, ":"); colonIdx >= 0 && !strings.ContainsAny(creds[:colonIdx], "/?#") {
				return trimmed[:schemeIdx+3] + creds[:colonIdx+1] + "***@" + afterAt
			}
		}
	}
	return trimmed
}

// RedactText scrubs embedded URL/proxy passwords from arbitrary log or error text.
func RedactText(msg string) string {
	return scrubEmbeddedCreds(msg)
}

// RedactError formats err without exposing embedded proxy/URL passwords (such as *url.Error.URL).
func RedactError(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	var ue *url.Error
	if errors.As(err, &ue) && ue.URL != "" {
		msg = strings.ReplaceAll(msg, ue.URL, RedactProxyURL(ue.URL))
	}
	return scrubEmbeddedCreds(msg)
}

func scrubEmbeddedCreds(msg string) string {
	idx := 0
	var b strings.Builder
	for idx < len(msg) {
		rel := strings.Index(msg[idx:], "://")
		if rel < 0 {
			b.WriteString(msg[idx:])
			break
		}
		schemeEnd := idx + rel + 3
		b.WriteString(msg[idx:schemeEnd])
		rest := msg[schemeEnd:]
		end := strings.IndexAny(rest, " \t\r\n\"'")
		segment := rest
		if end >= 0 {
			segment = rest[:end]
		}
		// Only rewrite credentials when url.Parse finds userinfo or fails to parse (e.g. unencoded '/', '?', '#' in password).
		// Wait: if url.Parse succeeds with u.User == nil and path contains '@', do not touch normal paths.
		fullSeg := msg[idx+rel : schemeEnd+len(segment)]
		if strings.HasPrefix(fullSeg, "://") {
			fullSeg = "http" + fullSeg
		}
		u, parseErr := url.Parse(fullSeg)
		if parseErr != nil || (u != nil && u.User != nil) {
			if at := strings.LastIndex(segment, "@"); at >= 0 {
				creds := segment[:at]
				if colon := strings.Index(creds, ":"); colon >= 0 && !strings.ContainsAny(creds[:colon], "/?#") {
					b.WriteString(creds[:colon+1])
					b.WriteString("***@")
					b.WriteString(segment[at+1:])
					idx = schemeEnd + len(segment)
					continue
				}
			}
		}
		b.WriteString(segment)
		idx = schemeEnd + len(segment)
	}
	return b.String()
}
