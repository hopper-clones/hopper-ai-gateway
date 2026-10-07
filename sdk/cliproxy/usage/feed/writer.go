package feed

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	log "github.com/sirupsen/logrus"
)

const (
	// hourLayout and hourSuffix form the hourly file name "<YYYY-MM-DDTHH>Z.jsonl".
	hourLayout = "2006-01-02T15"
	hourSuffix = "Z.jsonl"

	defaultQueueSize     = 8192
	defaultBatchSize     = 500
	defaultFlushInterval = 250 * time.Millisecond
	dropWarnEvery        = 1000
)

type entry struct {
	file string
	line []byte
}

// feedFile is the subset of *os.File the writer needs; tests inject failures.
type feedFile interface {
	io.Writer
	Sync() error
	Close() error
	Truncate(size int64) error
	Seek(offset int64, whence int) (int64, error)
}

func openAppendFile(path string) (feedFile, error) {
	return os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
}

// Writer appends events to hourly JSONL files. Write never blocks the caller:
// events queue on a buffered channel and a single goroutine batches them every
// flush interval or every batchSize events with one fsync per batch.
type Writer struct {
	dir       string
	queue     chan entry
	batchSize int
	now       func() time.Time
	ticks     <-chan time.Time
	ticker    *time.Ticker
	paused    bool
	onRotate  func()

	stop      chan struct{}
	done      chan struct{}
	flushReq  chan chan struct{}
	closeOnce sync.Once
	// stateMu orders Write against Close: writers hold it shared while they
	// check closed and enqueue; Close holds it exclusively to flip closed.
	stateMu sync.RWMutex
	closed  bool
	dropped atomic.Uint64

	openFile feedFile
	fileName string
	open     func(path string) (feedFile, error)
}

// WriterOption adjusts a Writer; the defaults suit production.
type WriterOption func(*Writer)

// WithClock replaces the wall clock used to name hourly files.
func WithClock(now func() time.Time) WriterOption {
	return func(w *Writer) {
		if now != nil {
			w.now = now
		}
	}
}

// WithTicker replaces the flush ticker so tests can drive batching deterministically.
func WithTicker(ticks <-chan time.Time) WriterOption {
	return func(w *Writer) { w.ticks = ticks }
}

// WithQueueSize sets the buffered channel capacity.
func WithQueueSize(size int) WriterOption {
	return func(w *Writer) {
		if size > 0 {
			w.queue = make(chan entry, size)
		}
	}
}

// withPausedLoop keeps the batching goroutine from starting (tests only).
func withPausedLoop() WriterOption {
	return func(w *Writer) { w.paused = true }
}

// withFileOpener replaces how hourly files are opened (tests only).
func withFileOpener(open func(path string) (feedFile, error)) WriterOption {
	return func(w *Writer) { w.open = open }
}

// withRotateHook runs fn on the writer goroutine whenever a new hourly file opens.
func withRotateHook(fn func()) WriterOption {
	return func(w *Writer) { w.onRotate = fn }
}

// NewWriter creates the directory when needed and starts the batching goroutine.
func NewWriter(dir string, opts ...WriterOption) (*Writer, error) {
	if dir == "" {
		return nil, fmt.Errorf("usage feed: directory is empty")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("usage feed: create %s: %w", dir, err)
	}
	w := &Writer{
		dir:       dir,
		queue:     make(chan entry, defaultQueueSize),
		batchSize: defaultBatchSize,
		now:       time.Now,
		stop:      make(chan struct{}),
		done:      make(chan struct{}),
		flushReq:  make(chan chan struct{}),
		open:      openAppendFile,
	}
	for _, opt := range opts {
		opt(w)
	}
	if w.ticks == nil {
		w.ticker = time.NewTicker(defaultFlushInterval)
		w.ticks = w.ticker.C
	}
	if w.paused {
		close(w.done)
		return w, nil
	}
	go w.run()
	return w, nil
}

// Write queues one event. It returns false when the event was dropped because
// the queue is full or the writer is closed; every drop is counted and warned.
func (w *Writer) Write(event any) bool {
	if w == nil {
		return false
	}
	line, err := json.Marshal(event)
	if err != nil {
		log.WithError(err).Warn("usage feed: event not serializable")
		return false
	}
	w.stateMu.RLock()
	defer w.stateMu.RUnlock()
	if w.closed {
		w.recordDrop()
		return false
	}
	select {
	case w.queue <- entry{file: hourFile(w.now()), line: line}:
		return true
	default:
		w.recordDrop()
		return false
	}
}

func (w *Writer) isClosed() bool {
	w.stateMu.RLock()
	defer w.stateMu.RUnlock()
	return w.closed
}

func (w *Writer) recordDrop() {
	dropped := w.dropped.Add(1)
	if dropped == 1 || dropped%dropWarnEvery == 0 {
		log.WithField("dropped_total", dropped).Warn("usage feed: queue full, event dropped")
	}
}

// Dropped reports how many events were dropped since the writer started.
func (w *Writer) Dropped() uint64 {
	if w == nil {
		return 0
	}
	return w.dropped.Load()
}

// Flush writes every queued event to disk before returning.
func (w *Writer) Flush() {
	if w == nil || w.isClosed() {
		return
	}
	if w.paused {
		w.drainAndFlush()
		return
	}
	ack := make(chan struct{})
	select {
	case w.flushReq <- ack:
		<-ack
	case <-w.done:
	}
}

// Close drains the queue, flushes it and closes the current file.
func (w *Writer) Close() error {
	if w == nil {
		return nil
	}
	var errClose error
	w.closeOnce.Do(func() {
		// Exclusive: no Write can be between its closed check and its enqueue,
		// so the drain below sees every accepted event.
		w.stateMu.Lock()
		w.closed = true
		w.stateMu.Unlock()
		close(w.stop)
		<-w.done
		if w.paused {
			w.drainAndFlush()
		}
		if w.ticker != nil {
			w.ticker.Stop()
		}
		errClose = w.closeFile()
	})
	return errClose
}

func (w *Writer) run() {
	defer close(w.done)
	batch := make([]entry, 0, w.batchSize)
	for {
		select {
		case item := <-w.queue:
			batch = append(batch, item)
			if len(batch) >= w.batchSize {
				w.flush(batch)
				batch = batch[:0]
			}
		case <-w.ticks:
			if len(batch) > 0 {
				w.flush(batch)
				batch = batch[:0]
			}
		case ack := <-w.flushReq:
			batch = w.drainInto(batch)
			w.flush(batch)
			batch = batch[:0]
			close(ack)
		case <-w.stop:
			batch = w.drainInto(batch)
			w.flush(batch)
			return
		}
	}
}

func (w *Writer) drainAndFlush() {
	batch := w.drainInto(nil)
	w.flush(batch)
}

func (w *Writer) drainInto(batch []entry) []entry {
	for {
		select {
		case item := <-w.queue:
			batch = append(batch, item)
		default:
			return batch
		}
	}
}

// flush writes the batch grouped by hourly file, one write and one fsync per file touched.
func (w *Writer) flush(batch []entry) {
	if len(batch) == 0 {
		return
	}
	var buf bytes.Buffer
	current := batch[0].file
	for _, item := range batch {
		if item.file != current {
			w.writeFile(current, buf.Bytes())
			buf.Reset()
			current = item.file
		}
		buf.Write(item.line)
		buf.WriteByte('\n')
	}
	w.writeFile(current, buf.Bytes())
}

func (w *Writer) writeFile(name string, data []byte) {
	if len(data) == 0 {
		return
	}
	if err := w.ensureFile(name); err != nil {
		log.WithError(err).WithField("file", name).Error("usage feed: open hourly file")
		return
	}
	start, errSeek := w.openFile.Seek(0, io.SeekEnd)
	if errSeek != nil {
		log.WithError(errSeek).WithField("file", name).Error("usage feed: locate end of hourly file")
		return
	}
	if _, err := w.openFile.Write(data); err != nil {
		log.WithError(err).WithField("file", name).Error("usage feed: write batch")
		// A torn batch would corrupt the next line: cut back to the last good offset.
		if errTruncate := w.openFile.Truncate(start); errTruncate != nil {
			log.WithError(errTruncate).WithField("file", name).Error("usage feed: truncate torn batch")
		}
		return
	}
	if err := w.openFile.Sync(); err != nil {
		log.WithError(err).WithField("file", name).Error("usage feed: fsync batch")
	}
}

func (w *Writer) ensureFile(name string) error {
	if w.openFile != nil && w.fileName == name {
		return nil
	}
	if err := w.closeFile(); err != nil {
		log.WithError(err).WithField("file", w.fileName).Warn("usage feed: close previous hourly file")
	}
	file, err := w.open(filepath.Join(w.dir, name))
	if err != nil {
		return err
	}
	w.openFile, w.fileName = file, name
	if w.onRotate != nil {
		w.onRotate()
	}
	return nil
}

func (w *Writer) closeFile() error {
	if w.openFile == nil {
		return nil
	}
	err := w.openFile.Close()
	w.openFile, w.fileName = nil, ""
	return err
}

// hourFile names the hourly file holding events written at the given time.
func hourFile(at time.Time) string {
	return at.UTC().Format(hourLayout) + hourSuffix
}
