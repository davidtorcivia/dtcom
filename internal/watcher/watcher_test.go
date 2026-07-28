package watcher

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWatcherFiresOnChange(t *testing.T) {
	dir := t.TempDir()
	events := make(chan string, 8)
	w, err := Watch(dir, events)
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	defer w.Close()
	// small delay to ensure watch is registered before writing
	time.Sleep(100 * time.Millisecond)
	if err := os.WriteFile(filepath.Join(dir, "a.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	select {
	case <-events:
		// got an event
	case <-time.After(2 * time.Second):
		t.Fatal("no event within 2s")
	}
}
