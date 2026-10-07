package feed

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"
)

const cursorsFileName = "cursors.json"

var consumerNamePattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// AckedCursor is the last cursor a consumer confirmed it has durably processed.
type AckedCursor struct {
	Cursor  string `json:"cursor"`
	AckedAt Time   `json:"acked_at"`
}

// Cursors persists per-consumer acked cursors in usage-feed/cursors.json.
type Cursors struct {
	path string
	mu   sync.Mutex
	now  func() time.Time
}

// NewCursors binds the cursor file inside dir.
func NewCursors(dir string) *Cursors {
	return &Cursors{path: filepath.Join(dir, cursorsFileName), now: time.Now}
}

// Ack records the consumer's cursor with an atomic write (temp file + rename).
func (c *Cursors) Ack(consumer, cursor string) error {
	if c == nil {
		return errors.New("usage feed: cursors unavailable")
	}
	if !consumerNamePattern.MatchString(consumer) {
		return fmt.Errorf("usage feed: invalid consumer name %q", consumer)
	}
	file, offset, err := ParseCursor(cursor)
	if err != nil {
		return err
	}
	if _, errStat := os.Stat(filepath.Join(filepath.Dir(c.path), file)); errStat != nil {
		return fmt.Errorf("usage feed: cursor names a file that does not exist: %s", file)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	current, err := c.loadLocked()
	if err != nil {
		return err
	}
	if previous, exists := current[consumer]; exists {
		prevFile, prevOffset, errPrev := ParseCursor(previous.Cursor)
		if errPrev == nil && (file < prevFile || (file == prevFile && offset < prevOffset)) {
			return fmt.Errorf("usage feed: cursor %s moves backwards from %s", cursor, previous.Cursor)
		}
	}
	current[consumer] = AckedCursor{Cursor: cursor, AckedAt: Time(c.now())}
	return c.saveLocked(current)
}

// ErrUnknownConsumer reports a delete for a consumer that never acked.
var ErrUnknownConsumer = errors.New("usage feed: unknown consumer")

// Delete forgets a consumer so it no longer holds retention.
func (c *Cursors) Delete(consumer string) error {
	if c == nil {
		return errors.New("usage feed: cursors unavailable")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	current, err := c.loadLocked()
	if err != nil {
		return err
	}
	if _, exists := current[consumer]; !exists {
		return ErrUnknownConsumer
	}
	delete(current, consumer)
	return c.saveLocked(current)
}

// List returns every consumer's acked cursor.
func (c *Cursors) List() (map[string]AckedCursor, error) {
	if c == nil {
		return map[string]AckedCursor{}, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.loadLocked()
}

func (c *Cursors) loadLocked() (map[string]AckedCursor, error) {
	data, err := os.ReadFile(c.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]AckedCursor{}, nil
		}
		return nil, fmt.Errorf("usage feed: read %s: %w", c.path, err)
	}
	cursors := map[string]AckedCursor{}
	if len(data) == 0 {
		return cursors, nil
	}
	if err := json.Unmarshal(data, &cursors); err != nil {
		return nil, fmt.Errorf("usage feed: decode %s: %w", c.path, err)
	}
	return cursors, nil
}

func (c *Cursors) saveLocked(cursors map[string]AckedCursor) error {
	data, err := json.MarshalIndent(cursors, "", "  ")
	if err != nil {
		return fmt.Errorf("usage feed: encode cursors: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(c.path), 0o700); err != nil {
		return fmt.Errorf("usage feed: create directory: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(c.path), cursorsFileName+".*.tmp")
	if err != nil {
		return fmt.Errorf("usage feed: create temp cursors file: %w", err)
	}
	tmpName := tmp.Name()
	if err := writeAndSync(tmp, data); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, c.path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("usage feed: replace cursors file: %w", err)
	}
	return nil
}

func writeAndSync(file *os.File, data []byte) error {
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return fmt.Errorf("usage feed: write cursors: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("usage feed: fsync cursors: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("usage feed: close cursors: %w", err)
	}
	return nil
}
