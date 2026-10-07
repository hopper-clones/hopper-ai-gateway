package management

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/sync/singleflight"
)

// capacityBridgeTimeout bounds the Capacity reader child process. It is a local
// child process deadline, not an upstream network timeout. Tests shorten it.
var capacityBridgeTimeout = 20 * time.Second

// capacityFlight lets concurrent snapshot requests share one reader run.
var capacityFlight singleflight.Group

// capacityFlightJoined, when set, is called once a request has registered with
// the shared run (tests use it to prove the overlap deterministically).
var capacityFlightJoined func()

type capacityRun struct {
	status int
	body   []byte
}

// GetCapacitySnapshot reads the explicitly configured public Capacity owner surface.
// Register only behind management authentication. The actual peer must be local.
func (h *Handler) GetCapacitySnapshot(c *gin.Context) {
	if !loopbackPeer(c) {
		c.JSON(http.StatusForbidden, gin.H{"code": "CAPACITY_LOCAL_ONLY"})
		return
	}
	script := os.Getenv("HOPPER_AI_CAPACITY_READER")
	if !filepath.IsAbs(script) || os.Getenv("HOPPER_AI_CAPACITY_STATE") == "" || os.Getenv("HOPPER_AI_CAPACITY_PACKAGE") == "" {
		c.JSON(http.StatusServiceUnavailable, gin.H{"code": "CAPACITY_UNCONFIGURED"})
		return
	}
	bun := os.Getenv("HOPPER_AI_CAPACITY_BUN")
	if bun == "" {
		var err error
		if bun, err = exec.LookPath("bun"); err != nil || bun == "" {
			c.JSON(http.StatusServiceUnavailable, gin.H{"code": "CAPACITY_READER_UNAVAILABLE"})
			return
		}
	}
	// DoChan registers this request with the in-flight run synchronously.
	results := capacityFlight.DoChan("snapshot", func() (any, error) {
		return runCapacityReader(bun, script), nil
	})
	if capacityFlightJoined != nil {
		capacityFlightJoined()
	}
	result := <-results
	run, ok := result.Val.(capacityRun)
	if !ok {
		c.JSON(http.StatusServiceUnavailable, gin.H{"code": "CAPACITY_OWNER_UNAVAILABLE"})
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Data(run.status, "application/json", run.body)
}

// capacityExec runs the reader and returns its stdout and stderr. Tests replace
// it to drive overlap and deadlines deterministically.
var capacityExec = execCapacityReader

func execCapacityReader(ctx context.Context, bun, script string) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, bun, script)
	cmd.Env = capacityEnv(os.Environ())
	cmd.WaitDelay = time.Second
	configureCapacityProcess(cmd)
	var output, diagnostic bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &diagnostic
	err := cmd.Run()
	return output.Bytes(), diagnostic.Bytes(), err
}

// runCapacityReader executes the reader once with a scrubbed environment and a
// deadline, and maps its outcome to the HTTP response shared by all waiters.
func runCapacityReader(bun, script string) capacityRun {
	ctx, cancel := context.WithTimeout(context.Background(), capacityBridgeTimeout)
	defer cancel()
	output, diagnostic, err := capacityExec(ctx, bun, script)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return capacityJSON(http.StatusGatewayTimeout, "CAPACITY_TIMEOUT")
		}
		code := "CAPACITY_OWNER_UNAVAILABLE"
		var failure struct {
			Code string `json:"code"`
		}
		if json.Unmarshal(diagnostic, &failure) == nil {
			switch failure.Code {
			case "CAPACITY_UNCONFIGURED", "CAPACITY_INVALID_PACKAGE", "CAPACITY_INVALID_SESSION", "CAPACITY_INCOMPLETE_HISTORY":
				code = failure.Code
			}
		}
		return capacityJSON(http.StatusServiceUnavailable, code)
	}
	var result struct {
		SchemaVersion string            `json:"schemaVersion"`
		Accounts      []json.RawMessage `json:"accounts"`
	}
	if json.Unmarshal(output, &result) != nil || result.SchemaVersion != "hopper.gateway-capacity.v1" || result.Accounts == nil {
		return capacityJSON(http.StatusBadGateway, "CAPACITY_INVALID_RESPONSE")
	}
	return capacityRun{status: http.StatusOK, body: output}
}

func capacityJSON(status int, code string) capacityRun {
	body, _ := json.Marshal(gin.H{"code": code})
	return capacityRun{status: status, body: body}
}

// capacityEnv keeps only PATH, HOME and the HOPPER_AI_CAPACITY_* selection, and
// pins account discovery off. Nothing else from the gateway's environment reaches
// the reader.
func capacityEnv(environ []string) []string {
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
