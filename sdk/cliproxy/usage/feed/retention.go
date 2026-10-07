package feed

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// RetentionAge is how long hourly files are kept once every consumer is past them.
const RetentionAge = 30 * 24 * time.Hour

// Prune deletes hourly files older than RetentionAge, but only those every
// consumer's acked cursor has passed. With no consumers nothing is deleted:
// unacked data is never discarded. It returns the deleted file names.
func Prune(dir string, now time.Time, acked map[string]AckedCursor) ([]string, error) {
	if len(acked) == 0 {
		return nil, nil
	}
	files, err := listFiles(dir)
	if err != nil {
		return nil, err
	}
	var deleted []string
	for _, name := range files {
		hour, errParse := time.Parse(hourLayout, strings.TrimSuffix(name, hourSuffix))
		if errParse != nil {
			continue
		}
		// The file may receive events until the end of its hour.
		if now.Sub(hour.Add(time.Hour)) < RetentionAge {
			continue
		}
		path := filepath.Join(dir, name)
		if !allConsumersPast(acked, name, path) {
			continue
		}
		if errRemove := os.Remove(path); errRemove != nil {
			return deleted, fmt.Errorf("usage feed: delete %s: %w", name, errRemove)
		}
		deleted = append(deleted, name)
	}
	return deleted, nil
}

// allConsumersPast reports whether every acked cursor is beyond the named file:
// either in a later file or at the end of this one.
func allConsumersPast(acked map[string]AckedCursor, name, path string) bool {
	for _, entry := range acked {
		file, offset, err := ParseCursor(entry.Cursor)
		if err != nil || file < name {
			return false
		}
		if file == name {
			info, errStat := os.Stat(path)
			if errStat != nil || offset < info.Size() {
				return false
			}
		}
	}
	return true
}
