package feed

import (
	"encoding/json"
	"time"
)

// Scan calls fn for every complete event in the hourly files from from's hour
// onwards, oldest first, and stops after max events (max <= 0 means no cap).
// It reports whether it stopped at the cap. Events before from inside the first
// hour are passed too; callers filter by their own "at".
func Scan(dir string, from time.Time, max int, fn func(json.RawMessage)) (bool, error) {
	cursor := FormatCursor(hourFile(from), 0)
	seen := 0
	for {
		page, err := Read(dir, cursor, MaxReadLimit)
		if err != nil {
			return false, err
		}
		for _, event := range page.Events {
			if max > 0 && seen >= max {
				return true, nil
			}
			fn(event)
			seen++
		}
		if !page.HasMore || page.NextCursor == cursor || page.NextCursor == "" {
			return false, nil
		}
		cursor = page.NextCursor
	}
}

// Scan reads the store's directory; see the package-level Scan.
func (s *Store) Scan(from time.Time, max int, fn func(json.RawMessage)) (bool, error) {
	if s == nil {
		return false, nil
	}
	return Scan(s.dir, from, max, fn)
}
