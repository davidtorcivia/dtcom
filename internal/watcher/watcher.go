// Package watcher watches a directory tree and sends change events.
package watcher

import (
	"os"
	"path/filepath"

	"github.com/fsnotify/fsnotify"
)

type Watcher struct {
	fw *fsnotify.Watcher
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
	go func() {
		for {
			select {
			case ev, ok := <-fw.Events:
				if !ok {
					return
				}
				if ev.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Remove|fsnotify.Rename) != 0 {
					select {
					case events <- ev.Name:
					default:
						// coalesce: drop if consumer isn't keeping up
					}
				}
			case _, ok := <-fw.Errors:
				if !ok {
					return
				}
			}
		}
	}()
	return &Watcher{fw: fw}, nil
}

func (w *Watcher) Close() error { return w.fw.Close() }
