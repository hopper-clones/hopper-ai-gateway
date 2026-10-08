package capacitycodex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// readerTimeout bounds the Capacity reader child process. It is a local child
// process deadline, not an upstream network timeout.
var readerTimeout = 20 * time.Second

const readerSchema = "hopper.gateway-capacity-codex.v1"

// Account is one Codex account reported by the Capacity reader. The private
// fields stay inside the gateway process and are never logged or served.
type Account struct {
	// ID is the Capacity account hash, the same one the capacity bridge serves.
	ID      string  `json:"id"`
	Label   string  `json:"label"`
	Model   *string `json:"model"`
	Refused *string `json:"refused"`
	Account *struct {
		AccountRef        string `json:"accountRef"`
		Home              string `json:"home"`
		ExpectedEmail     string `json:"expectedEmail"`
		ProviderAccountID string `json:"providerAccountId"`
	} `json:"account"`
}

// Reader lists Capacity's Codex accounts.
type Reader func(ctx context.Context) ([]Account, error)

// ErrReaderUnconfigured means the Capacity reader environment is not set.
var ErrReaderUnconfigured = errors.New("CAPACITY_UNCONFIGURED")

// CommandReader runs `bun <HOPPER_AI_CAPACITY_READER> codex-accounts` with the
// same scrubbed environment as the capacity bridge.
func CommandReader() (Reader, error) {
	script := os.Getenv("HOPPER_AI_CAPACITY_READER")
	if !filepath.IsAbs(script) || os.Getenv("HOPPER_AI_CAPACITY_STATE") == "" || os.Getenv("HOPPER_AI_CAPACITY_PACKAGE") == "" {
		return nil, ErrReaderUnconfigured
	}
	bun := os.Getenv("HOPPER_AI_CAPACITY_BUN")
	if bun == "" {
		var err error
		if bun, err = exec.LookPath("bun"); err != nil || bun == "" {
			return nil, errors.New("CAPACITY_READER_UNAVAILABLE")
		}
	}
	return func(ctx context.Context) ([]Account, error) {
		ctx, cancel := context.WithTimeout(ctx, readerTimeout)
		defer cancel()
		cmd := exec.CommandContext(ctx, bun, script, "codex-accounts")
		cmd.Env = readerEnv(os.Environ())
		cmd.WaitDelay = time.Second
		configureProcess(cmd)
		var output, diagnostic bytes.Buffer
		cmd.Stdout, cmd.Stderr = &output, &diagnostic
		if err := cmd.Run(); err != nil {
			return nil, readerFailure(ctx, diagnostic.Bytes())
		}
		return parseAccounts(output.Bytes())
	}, nil
}

// readerFailure maps a failed run to a code; reader stderr is only a code.
func readerFailure(ctx context.Context, diagnostic []byte) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return errors.New("CAPACITY_TIMEOUT")
	}
	var failure struct {
		Code string `json:"code"`
	}
	if json.Unmarshal(diagnostic, &failure) == nil && strings.HasPrefix(failure.Code, "CAPACITY_") && len(failure.Code) <= 80 {
		return errors.New(failure.Code)
	}
	return errors.New("CAPACITY_OWNER_UNAVAILABLE")
}

func parseAccounts(data []byte) ([]Account, error) {
	var out struct {
		SchemaVersion string    `json:"schemaVersion"`
		Accounts      []Account `json:"accounts"`
	}
	if json.Unmarshal(data, &out) != nil || out.SchemaVersion != readerSchema || out.Accounts == nil {
		return nil, errors.New("CAPACITY_INVALID_RESPONSE")
	}
	return out.Accounts, nil
}

// readerEnv keeps only PATH, HOME and the HOPPER_AI_CAPACITY_* selection, and
// pins account discovery off.
func readerEnv(environ []string) []string {
	env := make([]string, 0, 8)
	for _, entry := range environ {
		name, _, found := strings.Cut(entry, "=")
		if !found {
			continue
		}
		if name == "PATH" || name == "HOME" || strings.HasPrefix(name, "HOPPER_AI_CAPACITY_") {
			env = append(env, entry)
		}
	}
	return append(env, "HOPPER_CAPACITY_ACCOUNT_DISCOVERY=off")
}
