package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"strings"
)

type BasicAuthConfig struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func CheckBasicAuth(cfg *BasicAuthConfig, authHeader string) bool {
	if cfg == nil || authHeader == "" {
		return false
	}

	if !strings.HasPrefix(authHeader, "Basic ") {
		return false
	}

	payload := authHeader[6:]
	decoded, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return false
	}

	parts := strings.SplitN(string(decoded), ":", 2)
	if len(parts) != 2 {
		return false
	}

	username := parts[0]
	password := parts[1]

	userHash := sha256.Sum256([]byte(username))
	passHash := sha256.Sum256([]byte(password))
	expectedUserHash := sha256.Sum256([]byte(cfg.Username))
	expectedPassHash := sha256.Sum256([]byte(cfg.Password))

	userOk := subtle.ConstantTimeCompare(userHash[:], expectedUserHash[:]) == 1
	passOk := subtle.ConstantTimeCompare(passHash[:], expectedPassHash[:]) == 1

	return userOk && passOk
}

func HasBasicAuth(cfg *BasicAuthConfig) bool {
	return cfg != nil && cfg.Username != "" && cfg.Password != ""
}
