package feed

import (
	"time"

	log "github.com/sirupsen/logrus"
)

// Store bundles the writer, the cursor file and retention for one feed directory.
type Store struct {
	dir     string
	writer  *Writer
	cursors *Cursors
	now     func() time.Time
}

// Open prepares dir, prunes retired files once and starts the writer.
// Retention runs again each time the writer opens a new hourly file.
func Open(dir string, opts ...WriterOption) (*Store, error) {
	store := &Store{dir: dir, cursors: NewCursors(dir), now: time.Now}
	probe := &Writer{now: time.Now}
	for _, opt := range opts {
		opt(probe)
	}
	store.now = probe.now
	store.cursors.now = probe.now
	writer, err := NewWriter(dir, append(opts, withRotateHook(store.pruneQuietly))...)
	if err != nil {
		return nil, err
	}
	store.writer = writer
	store.pruneQuietly()
	return store, nil
}

// Dir returns the feed directory.
func (s *Store) Dir() string {
	if s == nil {
		return ""
	}
	return s.dir
}

// Write queues one event without blocking; false means it was dropped.
func (s *Store) Write(event any) bool {
	if s == nil {
		return false
	}
	return s.writer.Write(event)
}

// Flush writes every queued event to disk.
func (s *Store) Flush() {
	if s != nil {
		s.writer.Flush()
	}
}

// Read returns one page of events from cursor.
func (s *Store) Read(cursor string, limit int) (Page, error) {
	return Read(s.dir, cursor, limit)
}

// Ack persists a consumer's cursor.
func (s *Store) Ack(consumer, cursor string) error {
	if s == nil {
		return nil
	}
	return s.cursors.Ack(consumer, cursor)
}

// DeleteCursor forgets a consumer's acked cursor.
func (s *Store) DeleteCursor(consumer string) error {
	if s == nil {
		return ErrUnknownConsumer
	}
	return s.cursors.Delete(consumer)
}

// Cursors lists every consumer's acked cursor.
func (s *Store) Cursors() (map[string]AckedCursor, error) {
	if s == nil {
		return map[string]AckedCursor{}, nil
	}
	return s.cursors.List()
}

// Prune deletes retired files every consumer has passed.
func (s *Store) Prune(now time.Time) ([]string, error) {
	acked, err := s.cursors.List()
	if err != nil {
		return nil, err
	}
	return Prune(s.dir, now, acked)
}

// Dropped reports how many events the writer dropped.
func (s *Store) Dropped() uint64 {
	if s == nil {
		return 0
	}
	return s.writer.Dropped()
}

// Close flushes and closes the writer.
func (s *Store) Close() error {
	if s == nil {
		return nil
	}
	return s.writer.Close()
}

func (s *Store) pruneQuietly() {
	deleted, err := s.Prune(s.now())
	if err != nil {
		log.WithError(err).Warn("usage feed: retention")
		return
	}
	if len(deleted) > 0 {
		log.WithField("files", deleted).Info("usage feed: retired hourly files")
	}
}
