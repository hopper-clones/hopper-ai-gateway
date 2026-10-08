// Package capacitycodex registers the Codex logins AI Capacity tracks as gateway
// credentials. Each login stays in its own official auth.json: tokens are read
// from that file when a request needs them, refreshed only once expired, and a
// refresh is written back to the same file in the official format.
package capacitycodex

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// authTokens are the token fields of an official Codex auth.json.
type authTokens struct {
	IDToken      string `json:"id_token"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	AccountID    string `json:"account_id"`
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

// writeAuthFile replaces the tokens and last_refresh of an official Codex
// auth.json, keeping every other key and the key order. The write is atomic
// (temporary file in the same directory, then rename) and keeps the file mode.
func writeAuthFile(path string, tokens authTokens, now time.Time) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	top, err := decodeObject(data)
	if err != nil {
		return err
	}
	var tokenMembers []member
	for _, m := range top {
		if m.key == "tokens" {
			if tokenMembers, err = decodeObject(m.value); err != nil {
				return err
			}
		}
	}
	tokenMembers = setString(tokenMembers, "id_token", tokens.IDToken)
	tokenMembers = setString(tokenMembers, "access_token", tokens.AccessToken)
	if tokens.RefreshToken != "" {
		tokenMembers = setString(tokenMembers, "refresh_token", tokens.RefreshToken)
	}
	if tokens.AccountID != "" {
		tokenMembers = setString(tokenMembers, "account_id", tokens.AccountID)
	}
	top = setMember(top, "tokens", encodeObject(tokenMembers))
	top = setString(top, "last_refresh", now.UTC().Format(time.RFC3339Nano))
	var out bytes.Buffer
	if err = json.Indent(&out, encodeObject(top), "", "  "); err != nil {
		return err
	}
	return replaceFile(path, out.Bytes(), info.Mode().Perm())
}

func replaceFile(path string, data []byte, perm os.FileMode) (err error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(name)
		}
	}()
	if err = tmp.Chmod(perm); err != nil {
		return err
	}
	if _, err = tmp.Write(data); err != nil {
		return err
	}
	if err = tmp.Sync(); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// member is one key of a JSON object, kept in file order.
type member struct {
	key   string
	value json.RawMessage
}

func decodeObject(data []byte) ([]member, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, errors.New("auth file is not a JSON object")
	}
	var members []member
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, ok := tok.(string)
		if !ok {
			return nil, errors.New("auth file has an invalid key")
		}
		var value json.RawMessage
		if err = dec.Decode(&value); err != nil {
			return nil, err
		}
		members = append(members, member{key: key, value: value})
	}
	if tok, err := dec.Token(); err != nil || tok != json.Delim('}') {
		return nil, errors.New("auth file is not a JSON object")
	}
	return members, nil
}

func encodeObject(members []member) []byte {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, m := range members {
		if i > 0 {
			buf.WriteByte(',')
		}
		key, _ := json.Marshal(m.key)
		buf.Write(key)
		buf.WriteByte(':')
		buf.Write(m.value)
	}
	buf.WriteByte('}')
	return buf.Bytes()
}

func setMember(members []member, key string, value json.RawMessage) []member {
	out := make([]member, 0, len(members)+1)
	found := false
	for _, m := range members {
		if m.key == key {
			m = member{key: key, value: value}
			found = true
		}
		out = append(out, m)
	}
	if !found {
		out = append(out, member{key: key, value: value})
	}
	return out
}

func setString(members []member, key, value string) []member {
	encoded, _ := json.Marshal(value)
	return setMember(members, key, encoded)
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
