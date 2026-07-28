package build

import (
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"davidtorcivia.com/dtcom/internal/assets"
	"davidtorcivia.com/dtcom/internal/feeds"
	"davidtorcivia.com/dtcom/internal/markdown"
	"davidtorcivia.com/dtcom/internal/siteconfig"
	"davidtorcivia.com/dtcom/internal/store"
)

type EngineConfig struct {
	ContentDir   string
	PostsDir     string // defaults to ContentDir+"/posts"
	PublicDir    string
	StaticDir    string
	Site         func() *siteconfig.Config
	Store        *store.Store
	TemplatesDir string

	// Assets fingerprints /static URLs. Optional: when nil the engine makes
	// its own over StaticDir. Pass a shared one so the admin templates, which
	// render outside this package, see the same hashes after a rebuild.
	Assets *assets.Fingerprinter
}

type Engine struct {
	cfg    EngineConfig
	mu     sync.Mutex
	tmpls  templateStore
	assets *assets.Fingerprinter
}

// NewEngine builds an engine and loads its templates. A template parse error
// is returned rather than swallowed: every page render would fail on a nil
// template, and the failure is far easier to act on at startup than as a
// nil-pointer panic on the first request.
func NewEngine(cfg EngineConfig) (*Engine, error) {
	if cfg.PostsDir == "" {
		cfg.PostsDir = filepath.Join(cfg.ContentDir, "posts")
	}
	if cfg.Assets == nil {
		cfg.Assets = assets.New(cfg.StaticDir)
	}
	e := &Engine{cfg: cfg, assets: cfg.Assets}
	if err := e.tmpls.Load(cfg.TemplatesDir, helperFuncs(e.assets)); err != nil {
		return nil, fmt.Errorf("load templates from %s: %w", cfg.TemplatesDir, err)
	}
	return e, nil
}

// Rebuild regenerates the entire public/ directory. Safe to call concurrently;
// rebuilds serialize and coalesce via the mutex.
func (e *Engine) Rebuild() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Templates are reloaded on every rebuild so an edit to templates/ takes
	// effect without a restart — which is what the docker-compose bind mount
	// for templates/ is for. A parse error leaves the previously-loaded set in
	// place rather than taking the site down.
	// Static files are bind-mounted in the container, so their hashes are
	// refreshed alongside the templates.
	e.assets.Refresh()
	if err := e.tmpls.Load(e.cfg.TemplatesDir, helperFuncs(e.assets)); err != nil {
		return fmt.Errorf("load templates: %w", err)
	}

	arts, err := LoadArticles(e.cfg.PostsDir)
	if err != nil {
		return fmt.Errorf("load articles: %w", err)
	}
	published := make([]Article, 0, len(arts))
	for _, a := range arts {
		if !a.Draft {
			published = append(published, a)
		}
	}

	// Every file this rebuild writes, so stale output can be pruned afterwards.
	written := newPathSet()

	for _, a := range published {
		if err := e.renderArticle(a, written); err != nil {
			return fmt.Errorf("render %s: %w", a.Slug, err)
		}
	}
	pages := []struct {
		name string
		fn   func(*pathSet) error
	}{
		{"home", func(w *pathSet) error { return e.renderHome(published, w) }},
		{"links", e.renderLinks},
		{"search", e.renderSearch},
		{"404", e.render404},
		{"feed", func(w *pathSet) error { return e.renderFeed(published, w) }},
		{"sitemap", func(w *pathSet) error { return e.renderSitemap(published, w) }},
		{"robots", e.renderRobots},
	}
	for _, p := range pages {
		if err := p.fn(written); err != nil {
			return fmt.Errorf("render %s: %w", p.name, err)
		}
	}

	// Remove output left over from deleted or newly-drafted posts.
	//
	// An earlier version emptied public/ before rendering. That worked, but it
	// meant every rebuild — including the one after each RSS poll — left the
	// whole site returning 404 for the time it took to re-render, since
	// requests are served straight off this directory with no coordination.
	// Writing first and pruning after keeps every page continuously readable.
	if err := e.prune(written); err != nil {
		return fmt.Errorf("prune public: %w", err)
	}

	// Reindex search. Convert build.Article → store.IndexedArticle.
	if e.cfg.Store != nil {
		indexed := make([]store.IndexedArticle, 0, len(published))
		for _, a := range published {
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

// pathSet records the files a rebuild produced, in cleaned absolute-ish form,
// so prune can tell current output from leftovers.
type pathSet struct {
	paths map[string]bool
}

func newPathSet() *pathSet { return &pathSet{paths: make(map[string]bool)} }

func (p *pathSet) add(path string) { p.paths[filepath.Clean(path)] = true }

func (p *pathSet) has(path string) bool { return p.paths[filepath.Clean(path)] }

// prune deletes files under PublicDir that this rebuild didn't write, then
// removes any directories left empty. This is what retires the page of a post
// that was deleted or flipped to draft.
func (e *Engine) prune(written *pathSet) error {
	root := e.cfg.PublicDir
	var dirs []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if path != root {
				dirs = append(dirs, path)
			}
			return nil
		}
		if !written.has(path) {
			return os.Remove(path)
		}
		return nil
	})
	if err != nil {
		return err
	}
	// Deepest first, so a directory emptied by the pass above is itself
	// removable. os.Remove on a non-empty directory fails, which is exactly
	// the guard we want.
	for i := len(dirs) - 1; i >= 0; i-- {
		_ = os.Remove(dirs[i])
	}
	return nil
}

// writeFile writes one output file and records it as current.
func (e *Engine) writeFile(path string, data []byte, written *pathSet) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return err
	}
	written.add(path)
	return nil
}

// renderPage renders a named template to a file and records it as current.
func (e *Engine) renderPage(name, outPath string, data any, written *pathSet) error {
	if err := e.tmpls.render(name, outPath, data); err != nil {
		return err
	}
	written.add(outPath)
	return nil
}

func (e *Engine) renderArticle(a Article, written *pathSet) error {
	htmlBody, err := markdown.Render(a.Body)
	if err != nil {
		return err
	}
	site := e.cfg.Site()
	ogImage, err := e.articleOGImage(a, site, written)
	if err != nil {
		return err
	}
	dir := filepath.Join(e.cfg.PublicDir, "posts", a.Slug)
	data := map[string]any{
		"Site":    site,
		"Article": a,
		"HTML":    htmlBody,
		"URL":     baseURL(site) + "/posts/" + a.Slug,
		"OGImage": ogImage,
		// KaTeX is ~600 KB of script and fonts. Most posts have no math, so
		// the page only pulls it in when there is something to typeset.
		"HasMath": markdown.HasMath(htmlBody),
	}
	if err := e.renderPage("article", filepath.Join(dir, "index.html"), data, written); err != nil {
		return err
	}
	// markdown variant — copy source file (frontmatter + body)
	src, err := os.ReadFile(a.SourcePath)
	if err != nil {
		return err
	}
	return e.writeFile(filepath.Join(e.cfg.PublicDir, "posts", a.Slug+".md"), src, written)
}

// renderHome renders the front page: bio + a date-desc index of published
// articles.
func (e *Engine) renderHome(published []Article, written *pathSet) error {
	site := e.cfg.Site()
	ogImage, err := e.siteOGImage(site, written)
	if err != nil {
		return err
	}
	return e.renderPage("home", filepath.Join(e.cfg.PublicDir, "index.html"), map[string]any{
		"Site":     site,
		"Articles": published,
		"OGImage":  ogImage,
	}, written)
}

// renderLinks renders the merged links index (manual + RSS-imported).
func (e *Engine) renderLinks(written *pathSet) error {
	var links []store.Link
	if e.cfg.Store != nil {
		var err error
		links, err = e.cfg.Store.ListLinks(500)
		if err != nil {
			return err
		}
	}
	site := e.cfg.Site()
	ogImage, err := e.siteOGImage(site, written)
	if err != nil {
		return err
	}
	return e.renderPage("links", filepath.Join(e.cfg.PublicDir, "links", "index.html"), map[string]any{
		"Site":    site,
		"Links":   links,
		"OGImage": ogImage,
	}, written)
}

// renderSearch renders the client-side search page. It is a static shell; the
// results come from /api/search at runtime.
func (e *Engine) renderSearch(written *pathSet) error {
	site := e.cfg.Site()
	ogImage, err := e.siteOGImage(site, written)
	if err != nil {
		return err
	}
	return e.renderPage("search", filepath.Join(e.cfg.PublicDir, "search", "index.html"), map[string]any{
		"Site":    site,
		"OGImage": ogImage,
	}, written)
}

// render404 renders the not-found page the server returns for unmatched routes.
func (e *Engine) render404(written *pathSet) error {
	site := e.cfg.Site()
	ogImage, err := e.siteOGImage(site, written)
	if err != nil {
		return err
	}
	return e.renderPage("notfound", filepath.Join(e.cfg.PublicDir, "404.html"), map[string]any{
		"Site":    site,
		"OGImage": ogImage,
	}, written)
}

// renderFeed renders the outbound RSS feed (feed.xml) of published articles.
func (e *Engine) renderFeed(published []Article, written *pathSet) error {
	site := e.cfg.Site()
	feedArts := make([]feeds.Article, 0, len(published))
	for _, a := range published {
		feedArts = append(feedArts, feeds.Article{
			Title:       a.Title,
			Slug:        a.Slug,
			Date:        a.Date,
			Description: a.Description,
		})
	}
	out, err := feeds.RenderFeed(site, feedArts)
	if err != nil {
		return err
	}
	return e.writeFile(filepath.Join(e.cfg.PublicDir, "feed.xml"), []byte(out), written)
}

// renderSitemap writes sitemap.xml covering the home, links, and each
// published article. URLs are XML-escaped and carry the article date as
// <lastmod>, which is what tells a crawler an existing page changed.
func (e *Engine) renderSitemap(published []Article, written *pathSet) error {
	base := baseURL(e.cfg.Site())
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	sb.WriteString(`<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">` + "\n")
	addURL := func(loc string, lastmod time.Time) {
		sb.WriteString("  <url><loc>" + xmlEscape(loc) + "</loc>")
		if !lastmod.IsZero() {
			sb.WriteString("<lastmod>" + lastmod.Format("2006-01-02") + "</lastmod>")
		}
		sb.WriteString("</url>\n")
	}
	// The home page's freshness is the newest post on it.
	var newest time.Time
	if len(published) > 0 {
		newest = published[0].Date
	}
	addURL(base+"/", newest)
	addURL(base+"/links", time.Time{})
	// /search is deliberately absent. The page carries <meta robots=noindex>
	// — it is a search box whose results are fetched client-side, so there is
	// nothing there for a crawler — and listing a noindex page in the sitemap
	// asks Google to crawl something it is simultaneously told to drop.
	for _, a := range published {
		addURL(base+"/posts/"+a.Slug, a.Date)
	}
	sb.WriteString("</urlset>\n")
	return e.writeFile(filepath.Join(e.cfg.PublicDir, "sitemap.xml"), []byte(sb.String()), written)
}

// renderRobots writes robots.txt allowing crawlers everywhere except the
// dynamic admin/api/mcp subtrees, and points at the sitemap.
func (e *Engine) renderRobots(written *pathSet) error {
	base := baseURL(e.cfg.Site())
	body := "User-agent: *\n" +
		"Disallow: /admin\n" +
		"Disallow: /api\n" +
		"Disallow: /mcp\n" +
		"Allow: /\n\n" +
		"Sitemap: " + base + "/sitemap.xml\n"
	return e.writeFile(filepath.Join(e.cfg.PublicDir, "robots.txt"), []byte(body), written)
}

// baseURL returns the site's canonical URL without a trailing slash, so
// callers can concatenate a path without producing a double slash.
func baseURL(site *siteconfig.Config) string {
	if site == nil {
		return ""
	}
	return strings.TrimRight(site.BaseURL, "/")
}

// xmlEscape escapes a string for inclusion in XML character data.
func xmlEscape(s string) string {
	var sb strings.Builder
	_ = xml.EscapeText(&sb, []byte(s))
	return sb.String()
}

var (
	fencedCodeRe = regexp.MustCompile("(?s)```.*?```")
	inlineCodeRe = regexp.MustCompile("`[^`]*`")
	emphasisRe   = regexp.MustCompile("[*_~]+")
	imageRe      = regexp.MustCompile(`!\[[^\]]*\]\([^)]*\)`)
	linkRe       = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	htmlTagRe    = regexp.MustCompile(`(?s)<[^>]*>`)
)

// stripMarkdown is a minimal markdown stripper for the search index body:
// drops emphasis markers, code fences, image syntax, and raw HTML tags,
// leaving plain words. Good enough for FTS5 — we don't need perfect text
// extraction.
//
// Stripping HTML matters beyond tidiness: markdown here is rendered with raw
// HTML enabled, and the indexed body is what FTS5 builds search snippets from.
// Leaving tags in would put markup into the excerpt shown on the search page.
// (The snippet is also escaped at query time; this is the other half.)
//
// The expressions are compiled once at package level rather than on every
// call — a rebuild runs this over every article.
func stripMarkdown(s string) string {
	s = fencedCodeRe.ReplaceAllString(s, " ")
	s = inlineCodeRe.ReplaceAllString(s, " ")
	s = htmlTagRe.ReplaceAllString(s, " ")
	s = emphasisRe.ReplaceAllString(s, "")
	s = imageRe.ReplaceAllString(s, " ")
	s = linkRe.ReplaceAllString(s, "$1")
	return s
}
