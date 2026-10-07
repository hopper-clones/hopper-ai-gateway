package feed

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadToleratesPartialLastLine(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "2026-10-07T12Z.jsonl")
	content := "{\"v\":1,\"id\":\"a\"}\n{\"v\":1,\"id\":\"b\"}\n{\"v\":1,\"id\":\"c"
	if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	page, err := Read(dir, "", 10)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if ids := eventIDs(t, page.Events); len(ids) != 2 || ids[0] != "a" || ids[1] != "b" {
		t.Fatalf("ids = %v, want [a b]", ids)
	}
	if page.HasMore {
		t.Fatal("partial line must not count as more data")
	}
	wantCursor := FormatCursor("2026-10-07T12Z.jsonl", int64(len("{\"v\":1,\"id\":\"a\"}\n{\"v\":1,\"id\":\"b\"}\n")))
	if page.NextCursor != wantCursor {
		t.Fatalf("cursor = %s, want %s", page.NextCursor, wantCursor)
	}
	f, err := os.OpenFile(file, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("\"}\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	page, err = Read(dir, page.NextCursor, 10)
	if err != nil {
		t.Fatalf("read after completion: %v", err)
	}
	if ids := eventIDs(t, page.Events); len(ids) != 1 || ids[0] != "c" {
		t.Fatalf("ids after completion = %v, want [c]", ids)
	}
}

func TestReadStartsAtOldestFileAndCrossesFiles(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("2026-10-07T13Z.jsonl", "{\"id\":\"c\"}\n{\"id\":\"d\"}\n")
	write("2026-10-07T12Z.jsonl", "{\"id\":\"a\"}\n{\"id\":\"b\"}\n")
	write("notes.txt", "ignored\n")

	page, err := Read(dir, "", 3)
	if err != nil {
		t.Fatal(err)
	}
	if ids := eventIDs(t, page.Events); len(ids) != 3 || ids[0] != "a" || ids[2] != "c" {
		t.Fatalf("ids = %v, want [a b c]", ids)
	}
	if !page.HasMore {
		t.Fatal("has_more should be true with one event left")
	}
	page, err = Read(dir, page.NextCursor, 3)
	if err != nil {
		t.Fatal(err)
	}
	if ids := eventIDs(t, page.Events); len(ids) != 1 || ids[0] != "d" {
		t.Fatalf("ids = %v, want [d]", ids)
	}
	if page.HasMore {
		t.Fatal("has_more should be false at the end")
	}
	// Reading again at the end stays put and returns nothing.
	again, err := Read(dir, page.NextCursor, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Events) != 0 || again.NextCursor != page.NextCursor {
		t.Fatalf("idle read = %+v", again)
	}
}

func TestReadSkipsToNextFileWhenCursorFileWasRetired(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "2026-10-07T13Z.jsonl"), []byte("{\"id\":\"c\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	page, err := Read(dir, FormatCursor("2026-10-01T00Z.jsonl", 999), 10)
	if err != nil {
		t.Fatal(err)
	}
	if ids := eventIDs(t, page.Events); len(ids) != 1 || ids[0] != "c" {
		t.Fatalf("ids = %v, want [c]", ids)
	}
}

func TestReadRejectsMalformedCursor(t *testing.T) {
	dir := t.TempDir()
	for _, cursor := range []string{"nofile", "../../etc/passwd:0", "2026-10-07T12Z.jsonl:-1", "2026-10-07T12Z.jsonl:abc"} {
		if _, err := Read(dir, cursor, 10); err == nil {
			t.Fatalf("cursor %q accepted", cursor)
		}
	}
}

func TestReadEmptyDirectory(t *testing.T) {
	page, err := Read(t.TempDir(), "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != 0 || page.NextCursor != "" || page.HasMore {
		t.Fatalf("empty page = %+v", page)
	}
}

func TestReadNeverAdvancesPastPartialLineOfNewestFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "2026-10-07T12Z.jsonl"), []byte("{\"id\":\"a\"}\n{\"id\":\"b"), 0o600); err != nil {
		t.Fatal(err)
	}
	page, err := Read(dir, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if ids := eventIDs(t, page.Events); len(ids) != 1 || page.NextCursor != FormatCursor("2026-10-07T12Z.jsonl", int64(len("{\"id\":\"a\"}\n"))) {
		t.Fatalf("page = %+v", page)
	}
	// Once a newer file exists the torn line will never complete: move on.
	if err := os.WriteFile(filepath.Join(dir, "2026-10-07T13Z.jsonl"), []byte("{\"id\":\"c\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	page, err = Read(dir, page.NextCursor, 10)
	if err != nil {
		t.Fatal(err)
	}
	if ids := eventIDs(t, page.Events); len(ids) != 1 || ids[0] != "c" || page.NextCursor != FormatCursor("2026-10-07T13Z.jsonl", int64(len("{\"id\":\"c\"}\n"))) {
		t.Fatalf("page after rotation = %+v", page)
	}
}
