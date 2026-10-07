package feed

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	log "github.com/sirupsen/logrus"
)

const (
	// DefaultReadLimit applies when a consumer passes no limit.
	DefaultReadLimit = 500
	// MaxReadLimit caps one page.
	MaxReadLimit = 5000
)

var hourFilePattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}Z\.jsonl$`)

// Page is one read of the feed.
type Page struct {
	Events     []json.RawMessage `json:"events"`
	NextCursor string            `json:"next_cursor"`
	HasMore    bool              `json:"has_more"`
}

// FormatCursor renders the opaque cursor "<file>:<byte offset>".
func FormatCursor(file string, offset int64) string {
	return file + ":" + strconv.FormatInt(offset, 10)
}

// ParseCursor validates and splits a cursor. Only hourly feed file names are accepted.
func ParseCursor(cursor string) (file string, offset int64, err error) {
	file, rawOffset, found := strings.Cut(cursor, ":")
	if !found || !hourFilePattern.MatchString(file) {
		return "", 0, fmt.Errorf("usage feed: malformed cursor %q", cursor)
	}
	offset, err = strconv.ParseInt(rawOffset, 10, 64)
	if err != nil || offset < 0 {
		return "", 0, fmt.Errorf("usage feed: malformed cursor offset %q", cursor)
	}
	return file, offset, nil
}

// listFiles returns the hourly feed files in dir sorted oldest first.
func listFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("usage feed: list %s: %w", dir, err)
	}
	files := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && hourFilePattern.MatchString(entry.Name()) {
			files = append(files, entry.Name())
		}
	}
	sort.Strings(files)
	return files, nil
}

// Read returns up to limit events starting at cursor. An empty cursor starts at
// the oldest retained file. A cursor naming a retired file resumes at the next
// file. A partially written last line is left for the next read.
func Read(dir, cursor string, limit int) (Page, error) {
	if limit <= 0 {
		limit = DefaultReadLimit
	}
	if limit > MaxReadLimit {
		limit = MaxReadLimit
	}
	files, err := listFiles(dir)
	if err != nil {
		return Page{}, err
	}
	file, offset := "", int64(0)
	if cursor != "" {
		if file, offset, err = ParseCursor(cursor); err != nil {
			return Page{}, err
		}
	}
	index := sort.SearchStrings(files, file)
	if index < len(files) && files[index] != file {
		// The cursor file was retired: resume at the next retained file.
		offset = 0
	}
	if index >= len(files) {
		return Page{Events: []json.RawMessage{}, NextCursor: cursor}, nil
	}
	page := Page{Events: make([]json.RawMessage, 0, limit)}
	for index < len(files) {
		name := files[index]
		events, next, err := readLines(filepath.Join(dir, name), offset, limit-len(page.Events))
		if err != nil {
			return Page{}, err
		}
		page.Events = append(page.Events, events...)
		page.NextCursor = FormatCursor(name, next)
		if len(page.Events) >= limit {
			page.HasMore = moreAfter(dir, files, index, next)
			return page, nil
		}
		if index+1 >= len(files) {
			return page, nil
		}
		index, offset = index+1, 0
	}
	return page, nil
}

// readLines reads complete lines from offset, stopping at limit or at a line
// without a trailing newline. It returns the events and the next byte offset.
func readLines(path string, offset int64, limit int) ([]json.RawMessage, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, offset, nil
		}
		return nil, 0, fmt.Errorf("usage feed: open %s: %w", path, err)
	}
	defer func() {
		if errClose := file.Close(); errClose != nil {
			log.WithError(errClose).Warn("usage feed: close feed file")
		}
	}()
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return nil, 0, fmt.Errorf("usage feed: seek %s: %w", path, err)
	}
	reader := bufio.NewReader(file)
	events := make([]json.RawMessage, 0, limit)
	for len(events) < limit {
		line, errRead := reader.ReadBytes('\n')
		if errRead != nil {
			// io.EOF without a newline is a partially written line; leave it.
			if !errors.Is(errRead, io.EOF) {
				return nil, 0, fmt.Errorf("usage feed: read %s: %w", path, errRead)
			}
			break
		}
		offset += int64(len(line))
		trimmed := strings.TrimRight(string(line), "\r\n")
		if trimmed == "" {
			continue
		}
		if !json.Valid([]byte(trimmed)) {
			log.WithField("file", filepath.Base(path)).Warn("usage feed: skipping corrupt line")
			continue
		}
		events = append(events, json.RawMessage(trimmed))
	}
	return events, offset, nil
}

// moreAfter reports whether complete data remains after offset in files[index] or in later files.
func moreAfter(dir string, files []string, index int, offset int64) bool {
	if rest, _, err := readLines(filepath.Join(dir, files[index]), offset, 1); err == nil && len(rest) > 0 {
		return true
	}
	for _, name := range files[index+1:] {
		if info, err := os.Stat(filepath.Join(dir, name)); err == nil && info.Size() > 0 {
			return true
		}
	}
	return false
}
