package build

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"davidtorcivia.com/dtcom/internal/markdown"
	"davidtorcivia.com/dtcom/internal/siteconfig"
	"davidtorcivia.com/dtcom/internal/store"
)

type EngineConfig struct {
	ContentDir   string
	PostsDir     string // defaults to ContentDir+"/posts"
	PublicDir    string
	Site         func() *siteconfig.Config
	Store        *store.Store
	TemplatesDir string
}

type Engine struct {
	cfg   EngineConfig
	mu    sync.Mutex
	tmpls templateStore
}

func NewEngine(cfg EngineConfig) *Engine {
	if cfg.PostsDir == "" {
		cfg.PostsDir = filepath.Join(cfg.ContentDir, "posts")
	}
	e := &Engine{cfg: cfg}
	_ = e.tmpls.Load(cfg.TemplatesDir, helperFuncs())
	return e
}

// Rebuild regenerates the entire public/ directory. Safe to call concurrently;
// rebuilds serialize and coalesce via the mutex.
func (e *Engine) Rebuild() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	arts, err := LoadArticles(e.cfg.PostsDir)
	if err != nil {
		return fmt.Errorf("load articles: %w", err)
	}
	if err := e.cleanPublic(); err != nil {
		return fmt.Errorf("clean public: %w", err)
	}
	for _, a := range arts {
		if a.Draft {
			continue
		}
		if err := e.renderArticle(a, arts); err != nil {
			return fmt.Errorf("render %s: %w", a.Slug, err)
		}
	}
	// NOTE: the following five render calls are added in Task 7.1 alongside
	// their templates. For Task 5.2, comment them out so the test (which only
	// checks article rendering) passes. Uncomment/implement in Task 7.1.
	//
	// if err := e.renderHome(arts); err != nil {
	// 	return fmt.Errorf("render home: %w", err)
	// }
	// if err := e.renderLinks(); err != nil {
	// 	return fmt.Errorf("render links: %w", err)
	// }
	// if err := e.renderFeed(arts); err != nil {
	// 	return fmt.Errorf("render feed: %w", err)
	// }
	// if err := e.renderSitemap(arts); err != nil {
	// 	return fmt.Errorf("render sitemap: %w", err)
	// }
	// if err := e.renderRobots(); err != nil {
	// 	return fmt.Errorf("render robots: %w", err)
	// }

	// Spec §15 step 4: reindex search. Convert build.Article → store.IndexedArticle.
	if e.cfg.Store != nil {
		indexed := make([]store.IndexedArticle, 0, len(arts))
		for _, a := range arts {
			if a.Draft {
				continue
			}
			indexed = append(indexed, store.IndexedArticle{
				Slug:        a.Slug,
				Title:       a.Title,
				Description: a.Description,
				Body:        stripMarkdown(a.Body),
				Tags:        strings.Join(a.Tags, ","),
			})
		}
		if err := e.cfg.Store.ReindexArticles(indexed); err != nil {
			return fmt.Errorf("reindex: %w", err)
		}
	}
	return nil
}

func (e *Engine) cleanPublic() error {
	if err := os.MkdirAll(e.cfg.PublicDir, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(e.cfg.PublicDir)
	if err != nil {
		return err
	}
	for _, en := range entries {
		_ = os.RemoveAll(filepath.Join(e.cfg.PublicDir, en.Name()))
	}
	return nil
}

func (e *Engine) renderArticle(a Article, all []Article) error {
	htmlBody, err := markdown.Render(a.Body)
	if err != nil {
		return err
	}
	site := e.cfg.Site()
	dir := filepath.Join(e.cfg.PublicDir, "posts", a.Slug)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data := map[string]any{
		"Site":    site,
		"Article": a,
		"HTML":    htmlBody,
		"URL":     strings.TrimRight(site.BaseURL, "/") + "/posts/" + a.Slug,
	}
	if err := e.tmpls.render("article", filepath.Join(dir, "index.html"), data); err != nil {
		return err
	}
	// markdown variant — copy source file (frontmatter + body)
	src, err := os.ReadFile(a.SourcePath)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(e.cfg.PublicDir, "posts", a.Slug+".md"), src, 0o644)
}

// stripMarkdown is a minimal markdown stripper for the search index body:
// drops emphasis markers, code fences, and image syntax, leaving plain words.
// Good enough for FTS5 — we don't need perfect text extraction.
func stripMarkdown(s string) string {
	s = regexp.MustCompile("(?s)```.*?```").ReplaceAllString(s, " ")
	s = regexp.MustCompile("`[^`]*`").ReplaceAllString(s, " ")
	s = regexp.MustCompile("[*_~]+").ReplaceAllString(s, "")
	s = regexp.MustCompile(`!\[[^\]]*\]\([^)]*\)`).ReplaceAllString(s, " ")
	s = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`).ReplaceAllString(s, "$1")
	return s
}
