// Package capacitycodex registers the Codex logins AI Capacity tracks as gateway
// credentials. Each login stays in its own official auth.json: tokens are read
// from that file when a request needs them. Only Capacity’s official client
// renews and persists credentials; the gateway never writes this file.
package capacitycodex

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

// authTokens are the token fields of an official Codex auth.json.
type authTokens struct {
	IDToken     string `json:"id_token"`
	AccessToken string `json:"access_token"`
	AccountID   string `json:"account_id"`
}

// fileStamp identifies one version of a file for change detection.
type fileStamp struct {
	modTime time.Time
	size    int64
}

func statStamp(path string) (fileStamp, error) {
	info, err := os.Stat(path)
	if err != nil {
		return fileStamp{}, err
	}
	return fileStamp{modTime: info.ModTime(), size: info.Size()}, nil
}

// readAuthFile reads the tokens of an official Codex auth.json.
func readAuthFile(path string) (authTokens, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return authTokens{}, err
	}
	var file struct {
		Tokens *authTokens `json:"tokens"`
	}
	if err = json.Unmarshal(data, &file); err != nil {
		return authTokens{}, errors.New("auth file is not valid JSON")
	}
	if file.Tokens == nil || file.Tokens.AccessToken == "" || file.Tokens.IDToken == "" {
		return authTokens{}, errors.New("auth file has no ChatGPT tokens")
	}
	return *file.Tokens, nil
}

// tokenClaims are the identity and expiry claims of a Codex JWT.
type tokenClaims struct {
	Exp   int64  `json:"exp"`
	Email string `json:"email"`
	Auth  struct {
		AccountID string `json:"chatgpt_account_id"`
		PlanType  string `json:"chatgpt_plan_type"`
	} `json:"https://api.openai.com/auth"`
}

// parseClaims decodes a JWT payload without verifying it; the file is the
// official login of a local account, and upstream verifies every token.
func parseClaims(token string) (tokenClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return tokenClaims{}, errors.New("token is not a JWT")
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return tokenClaims{}, fmt.Errorf("token payload: %w", err)
	}
	var claims tokenClaims
	if err = json.Unmarshal(payload, &claims); err != nil {
		return tokenClaims{}, fmt.Errorf("token claims: %w", err)
	}
	return claims, nil
}
