package petablocks

import (
	"crypto/subtle"
	"os"
	"strings"
)

// ValidateInternalToken checks if a Bearer token matches DISCOPANEL_INTERNAL_API_TOKEN
// or API_SECRET_TOKEN configured via environment variables.
// Uses constant-time comparison to prevent timing attacks.
func ValidateInternalToken(rawToken string) bool {
	if rawToken == "" {
		return false
	}

	candidates := []string{
		os.Getenv("DISCOPANEL_INTERNAL_API_TOKEN"),
		os.Getenv("API_SECRET_TOKEN"),
	}

	if prev := os.Getenv("API_SECRET_TOKEN_PREVIOUS"); prev != "" {
		for _, p := range strings.Split(prev, ",") {
			if trimmed := strings.TrimSpace(p); trimmed != "" {
				candidates = append(candidates, trimmed)
			}
		}
	}

	for _, secret := range candidates {
		if secret != "" && len(rawToken) == len(secret) {
			if subtle.ConstantTimeCompare([]byte(rawToken), []byte(secret)) == 1 {
				return true
			}
		}
	}

	return false
}
