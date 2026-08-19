package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"davidtorcivia.com/dtcom/internal/build"
	"davidtorcivia.com/dtcom/internal/siteconfig"
)

func TestVersionConstant(t *testing.T) {
	if Version == "" {
		t.Fatal("Version must not be empty")
	}
}

func newWatchEngine(t *testing.T) *build.Engine {
	t.Helper()
	content := t.TempDir()
	if err := os.MkdirAll(filepath.Join(content, "posts"), 0o755); err != nil {
		t.Fatal(err)
	}
	site := &siteconfig.Config{}
	e, err := build.NewEngine(build.EngineConfig{
		ContentDir:   content,
		PostsDir:     filepath.Join(content, "posts"),
		PublicDir:    t.TempDir(),
		StaticDir:    filepath.Join("..", "static"),
		TemplatesDir: filepath.Join("..", "templates"),
		Site:         func() *siteconfig.Config { return site },
	})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	return e
}

// A save through the admin UI, the API or MCP writes the post file and rebuilds
// before it returns; fsnotify then reports that same write to the watcher. Both
// halves used to rebuild, so every save rendered the whole site twice — and the
// cost is linear in the number of posts.
func TestWatchLoopSkipsRebuildAlreadyDone(t *testing.T) {
	engine := newWatchEngine(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := make(chan string, 4)
	go watchLoop(ctx, events, engine)

	// The order a save produces: the write lands, the watcher hears about it,
	// and the saving handler rebuilds on its own.
	events <- "posts/a.md"
	time.Sleep(100 * time.Millisecond)
	if err := engine.Rebuild(); err != nil {
		t.Fatal(err)
	}
	done := engine.BuildStartedAt()

	time.Sleep(rebuildDebounce + 500*time.Millisecond)
	if got := engine.BuildStartedAt(); !got.Equal(done) {
		t.Errorf("rebuilt again at %v after a rebuild at %v had already covered the change", got, done)
	}
}

// The other half of the same decision: a file changed by anything that does not
// rebuild for itself — an editor on the server, a git checkout, an rsync — must
// still trigger one, or the site silently stops matching its source.
func TestWatchLoopRebuildsOnExternalChange(t *testing.T) {
	engine := newWatchEngine(t)
	if err := engine.Rebuild(); err != nil {
		t.Fatal(err)
	}
	before := engine.BuildStartedAt()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := make(chan string, 4)
	go watchLoop(ctx, events, engine)

	events <- "posts/a.md"
	time.Sleep(rebuildDebounce + 500*time.Millisecond)
	if got := engine.BuildStartedAt(); !got.After(before) {
		t.Errorf("no rebuild after an outside change: still %v", got)
	}
}
