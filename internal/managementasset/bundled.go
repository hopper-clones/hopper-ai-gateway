package managementasset

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"io"
	"sync"
)

// The fork owns this single console. A Go build never substitutes an upstream download.
//
//go:embed console/management.html.gz
var bundledConsoleGZIP []byte

var bundledConsoleOnce sync.Once
var bundledConsoleHTML []byte
var bundledConsoleError error

// BundledConsole returns the console from the same build as the gateway.
func BundledConsole() ([]byte, error) {
	bundledConsoleOnce.Do(func() {
		reader, err := gzip.NewReader(bytes.NewReader(bundledConsoleGZIP))
		if err != nil {
			bundledConsoleError = err
			return
		}
		bundledConsoleHTML, bundledConsoleError = io.ReadAll(reader)
		if errClose := reader.Close(); bundledConsoleError == nil {
			bundledConsoleError = errClose
		}
	})
	return bundledConsoleHTML, bundledConsoleError
}
