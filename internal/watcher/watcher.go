// Package watcher watches a directory tree and sends change events.
package watcher

import (
	"os"
	"path/filepath"

	"github.com/fsnotify/fsnotify"
)

type Watcher struct {
	fw   *fsnotify.Watcher
	done chan struct{}
}

// Watch recursively watches dir and sends changed file paths to events.
// Returns immediately; the watcher runs in a goroutine until Close is called.
func Watch(dir string, events chan<- string) (*Watcher, error) {
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	err = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return fw.Add(path)
		}
		return nil
	})
	if err != nil {
		fw.Close()
		return nil, err
	}
	w := &Watcher{fw: fw, done: make(chan struct{})}
	go func() {
		for {
			select {
			case ev, ok := <-fw.Events:
				if !ok {
					return
				}
				// fsnotify is not recursive: a directory created after
				// startup needs its own watch, or files placed under it
				// would never be seen.
				if ev.Op&fsnotify.Create != 0 {
					if info, err := os.Stat(ev.Name); err == nil && info.IsDir() {
						_ = fw.Add(ev.Name)
					}
				}
				if ev.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Remove|fsnotify.Rename) != 0 {
					// Block rather than drop: the consumer is typically
					// inside a rebuild when the buffer fills, and a dropped
					// event means a write no rebuild ever sees. Blocking
					// coalesces instead — the debouncer rebuilds once the
					// burst drains.
					select {
					case events <- ev.Name:
					case <-w.done:
						return
					}
				}
			case _, ok := <-fw.Errors:
				if !ok {
					return
				}
			}
		}
	}()
	return w, nil
}

func (w *Watcher) Close() error {
	close(w.done)
	return w.fw.Close()
}
