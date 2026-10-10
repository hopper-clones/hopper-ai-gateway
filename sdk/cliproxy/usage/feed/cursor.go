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
	DefaultReadLimit = 500
	MaxReadLimit     = 5000
)

var hourFilePattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}Z\.jsonl$`)

// Coverage describes retained bytes, never a claim of complete historical usage.
// MissingCursor also covers a replay boundary file removed by retention.
type Coverage struct {
	Scope         string `json:"scope"`
	MissingCursor bool   `json:"missing_cursor"`
	TornFiles     int    `json:"torn_files"`
}

type Replay struct {
	Through  string `json:"through"`
	Complete bool   `json:"complete"`
}

// Page is one read of the feed. Replay is present only for a bounded read.
type Page struct {
	Events     []json.RawMessage `json:"events"`
	NextCursor string            `json:"next_cursor"`
	HasMore    bool              `json:"has_more"`
	Coverage   Coverage          `json:"coverage"`
	Replay     *Replay           `json:"replay,omitempty"`
}

func FormatCursor(file string, offset int64) string {
	return file + ":" + strconv.FormatInt(offset, 10)
}

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

// Read starts at cursor, or the oldest retained file when cursor is empty.
// A retired cursor resumes at the next retained file with an explicit gap.
func Read(dir, cursor string, limit int) (Page, error) {
	return readRange(dir, cursor, "", limit)
}

// ReadThrough replays only retained bytes up to the exclusive byte boundary
// through. Live writes cannot move that boundary. No acknowledgement is changed.
func ReadThrough(dir, cursor, through string, limit int) (Page, error) {
	if through == "" {
		return Page{}, errors.New("usage feed: replay requires a boundary")
	}
	return readRange(dir, cursor, through, limit)
}

func readRange(dir, cursor, through string, limit int) (Page, error) {
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
	endFile, endOffset := "", int64(0)
	if through != "" {
		if endFile, endOffset, err = ParseCursor(through); err != nil {
			return Page{}, err
		}
		if file > endFile || file == endFile && offset > endOffset {
			return Page{}, errors.New("usage feed: cursor exceeds replay boundary")
		}
	}
	page := Page{Events: make([]json.RawMessage, 0, limit), NextCursor: cursor, Coverage: Coverage{Scope: "retained-files"}}
	contains := func(name string) bool {
		i := sort.SearchStrings(files, name)
		return i < len(files) && files[i] == name
	}
	page.Coverage.MissingCursor = file != "" && !contains(file)
	if through != "" {
		page.Replay = &Replay{Through: through}
		page.Coverage.MissingCursor = page.Coverage.MissingCursor || !contains(endFile)
	}
	finishReplay := func() Page { page.NextCursor = through; page.HasMore = false; page.Replay.Complete = true; return page }
	index := sort.SearchStrings(files, file)
	if index < len(files) && files[index] != file {
		offset = 0
	}
	for index < len(files) {
		name := files[index]
		if through != "" && name > endFile {
			return finishReplay(), nil
		}
		end := int64(-1)
		if through != "" && name == endFile {
			end = endOffset
		}
		segment, err := readSegment(filepath.Join(dir, name), offset, end, limit-len(page.Events))
		if err != nil {
			return Page{}, err
		}
		if segment.missing {
			page.Coverage.MissingCursor = true
		}
		page.Events = append(page.Events, segment.events...)
		page.NextCursor = FormatCursor(name, segment.next)
		if end >= 0 && (segment.next == end || segment.missing) {
			return finishReplay(), nil
		}
		if len(page.Events) >= limit {
			if through != "" {
				page.HasMore = true
			} else {
				page.HasMore = moreAfter(dir, files, index, segment.next)
			}
			return page, nil
		}
		if segment.partial && (index+1 < len(files) || through != "" && name < endFile) {
			page.Coverage.TornFiles++
		}
		if through == "" && index+1 >= len(files) {
			return page, nil
		}
		index, offset = index+1, 0
	}
	if through != "" {
		return finishReplay(), nil
	}
	return page, nil
}

type segment struct {
	events  []json.RawMessage
	next    int64
	partial bool
	missing bool
}

// readSegment admits complete JSON lines only. Corruption fails the entire page,
// leaving the consumer's committed cursor unchanged. end=-1 reads to current EOF.
func readSegment(path string, offset, end int64, limit int) (segment, error) {
	result := segment{next: offset}
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			result.missing = true
			return result, nil
		}
		return result, fmt.Errorf("usage feed: open %s: %w", path, err)
	}
	defer func() {
		if e := file.Close(); e != nil {
			log.WithError(e).Warn("usage feed: close feed file")
		}
	}()
	info, err := file.Stat()
	if err != nil {
		return result, err
	}
	if offset > info.Size() || end > info.Size() {
		return result, errors.New("usage feed: cursor exceeds retained file size")
	}
	for _, boundary := range []int64{offset, end} {
		if boundary <= 0 {
			continue
		}
		b := []byte{0}
		if _, err = file.ReadAt(b, boundary-1); err != nil {
			return result, err
		}
		if b[0] != '\n' {
			return result, errors.New("usage feed: cursor is not a complete line boundary")
		}
	}
	if _, err = file.Seek(offset, io.SeekStart); err != nil {
		return result, err
	}
	var input io.Reader = file
	if end >= 0 {
		input = io.LimitReader(file, end-offset)
	}
	reader := bufio.NewReader(input)
	for len(result.events) < limit {
		line, readErr := reader.ReadBytes('\n')
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				return result, readErr
			}
			result.partial = len(line) > 0
			break
		}
		result.next += int64(len(line))
		trimmed := strings.TrimSpace(string(line))
		if trimmed == "" {
			continue
		}
		if !json.Valid([]byte(trimmed)) {
			return result, fmt.Errorf("usage feed: corrupt JSON line in %s at byte %d", filepath.Base(path), result.next-int64(len(line)))
		}
		result.events = append(result.events, json.RawMessage(trimmed))
	}
	return result, nil
}

func moreAfter(dir string, files []string, index int, offset int64) bool {
	rest, err := readSegment(filepath.Join(dir, files[index]), offset, -1, 1)
	// A corrupt next line must trigger another read and an explicit failure.
	if err != nil || len(rest.events) > 0 {
		return true
	}
	for _, name := range files[index+1:] {
		if info, err := os.Stat(filepath.Join(dir, name)); err == nil && info.Size() > 0 {
			return true
		}
	}
	return false
}
