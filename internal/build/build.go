package build

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"davidtorcivia.com/dtcom/internal/assets"
	"davidtorcivia.com/dtcom/internal/feeds"
	"davidtorcivia.com/dtcom/internal/markdown"
	"davidtorcivia.com/dtcom/internal/pgp"
	"davidtorcivia.com/dtcom/internal/siteconfig"
	"davidtorcivia.com/dtcom/internal/store"
)

const generationDir = ".dtcom-generations"

type EngineConfig struct {
	ContentDir   string
	PostsDir     string // defaults to ContentDir+"/posts"
	PublicDir    string
	StaticDir    string
	ImagesDir    string // uploaded images and their renditions
	Site         func() *siteconfig.Config
	Store        *store.Store
	TemplatesDir string

	// Assets fingerprints /static URLs. Optional: when nil the engine makes
	// its own over StaticDir. Pass a shared one so the admin templates, which
	// render outside this package, see the same hashes after a rebuild.
	Assets *assets.Fingerprinter

	// PGP looks up the contact address's public key during rebuild. Optional:
	// tests that must not talk to a keyserver leave it nil, and the site then
	// has no contact sheet or WKD files.
	PGP *pgp.Cache
}

type Engine struct {
	cfg    EngineConfig
	mu     sync.Mutex
	tmpls  templateStore
	assets *assets.Fingerprinter
	images *ImageIndex
	pgpOut *pgpMaterial

	// buildStart is when the running (or most recent) rebuild began reading
	// content, in Unix nanoseconds. See BuildStartedAt.
	buildStart  atomic.Int64
	lastBuild   atomic.Int64
	nextPublish atomic.Int64
	activeDir   atomic.Value // string
	lastErr     atomic.Value // string
	outputDir   string       // guarded by mu; set only while building a generation
	retiredDir  string       // previous generation, removed after the next switch
}

// BuildStartedAt reports when the running or most recent rebuild began reading
// content. It is the zero time until the first rebuild.
//
// The file watcher uses it to drop work it does not need to do. A save through
// the admin UI, the REST API or MCP writes the post file and rebuilds itself,
// and fsnotify reports that same write a moment later — so every save used to
// render the whole site twice. A rebuild that started after an event landed has
// already read what the event was telling us about, and the second pass renders
// byte-identical output.
//
// The clock is read before anything is loaded rather than after, so the
// comparison errs towards rebuilding again: a redundant rebuild costs
// milliseconds, a skipped one would serve stale HTML until the next write.
func (e *Engine) BuildStartedAt() time.Time {
	ns := e.buildStart.Load()
	if ns == 0 {
		return time.Time{}
	}
	return time.Unix(0, ns)
}

// Images is the engine's view of the images directory: which renditions exist
// beside each uploaded picture. The server uses it to backfill after an upload.
// Nil when the engine was built without an ImagesDir, which is the case in
// tests that render no images.
func (e *Engine) Images() *ImageIndex { return e.images }

// Preview renders an article with the live public theme without publishing it.
func (e *Engine) Preview(a Article) ([]byte, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	htmlBody, err := markdown.RenderWith(a.Body, e.images.Resolve)
	if err != nil {
		return nil, err
	}
	site := e.cfg.Site()
	return e.tmpls.execute("article", e.pageVars(site, map[string]any{
		"Article": a, "HTML": htmlBody, "URL": baseURL(site) + "/posts/" + a.Slug,
		"OGImage": a.Cover, "HasMath": markdown.HasMath(htmlBody), "Preview": true,
	}))
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
	e.activeDir.Store(cfg.PublicDir)
	e.lastErr.Store("")
	if cfg.ImagesDir != "" {
		e.images = NewImageIndex(cfg.ImagesDir)
	}
	if err := e.tmpls.Load(cfg.TemplatesDir, helperFuncs(e.assets)); err != nil {
		return nil, fmt.Errorf("load templates from %s: %w", cfg.TemplatesDir, err)
	}
	return e, nil
}

// Rebuild regenerates the entire public/ directory. Safe to call concurrently;
// rebuilds serialize and coalesce via the mutex.
func (e *Engine) Rebuild() (err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	defer func() {
		if err != nil {
			e.lastErr.Store(err.Error())
		} else {
			e.lastErr.Store("")
		}
	}()
	e.buildStart.Store(time.Now().UnixNano())

	generationRoot := filepath.Join(e.cfg.PublicDir, generationDir)
	if e.PublicDir() == e.cfg.PublicDir {
		_ = os.RemoveAll(generationRoot)
	}
	if err := os.MkdirAll(generationRoot, 0o755); err != nil {
		return fmt.Errorf("create build generation root: %w", err)
	}
	stage, err := os.MkdirTemp(generationRoot, "generation-")
	if err != nil {
		return fmt.Errorf("create build generation: %w", err)
	}
	if err := cloneTree(e.PublicDir(), stage); err != nil {
		_ = os.RemoveAll(stage)
		return fmt.Errorf("seed build generation: %w", err)
	}
	e.outputDir = stage
	succeeded := false
	defer func() {
		e.outputDir = ""
		if !succeeded {
			_ = os.RemoveAll(stage)
		}
	}()

	// Templates are reloaded on every rebuild so an edit to templates/ takes
	// effect without a restart — which is what the docker-compose bind mount
	// for templates/ is for. A parse error leaves the previously-loaded set in
	// place rather than taking the site down.
	// Static files are bind-mounted in the container, so their hashes are
	// refreshed alongside the templates.
	e.assets.Refresh()
	// Likewise the images: a picture uploaded since the last rebuild has
	// renditions the previous scan never saw.
	e.images.Refresh()
	if err := e.tmpls.Load(e.cfg.TemplatesDir, helperFuncs(e.assets)); err != nil {
		return fmt.Errorf("load templates: %w", err)
	}

	e.resolvePGP()

	arts, err := LoadArticles(e.cfg.PostsDir)
	if err != nil {
		return fmt.Errorf("load articles: %w", err)
	}
	published := make([]Article, 0, len(arts))
	now := time.Now()
	var next time.Time
	for _, a := range arts {
		if a.Draft {
			continue
		}
		if !a.PublishAt.IsZero() && a.PublishAt.After(now) {
			if next.IsZero() || a.PublishAt.Before(next) {
				next = a.PublishAt
			}
			continue
		}
		published = append(published, a)
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
		{"pgp", e.renderPGP},
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
	old := e.PublicDir()
	e.activeDir.Store(stage)
	if next.IsZero() {
		e.nextPublish.Store(0)
	} else {
		e.nextPublish.Store(next.Unix())
	}
	succeeded = true
	e.lastBuild.Store(time.Now().Unix())
	if e.retiredDir != "" && e.retiredDir != e.cfg.PublicDir {
		_ = os.RemoveAll(e.retiredDir)
	}
	if old != e.cfg.PublicDir {
		e.retiredDir = old
	}
	return nil
}

func cloneTree(src, dst string) error {
	if _, err := os.Stat(src); os.IsNotExist(err) {
		return nil
	}
	return filepath.WalkDir(src, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil || rel == "." {
			return err
		}
		if rel == generationDir && entry.IsDir() {
			return filepath.SkipDir
		}
		to := filepath.Join(dst, rel)
		if entry.IsDir() {
			return os.MkdirAll(to, 0o755)
		}
		if err := os.Link(path, to); err == nil {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(to, data, 0o644)
	})
}

// PublicDir is the complete generation currently served to readers.
func (e *Engine) PublicDir() string {
	if v := e.activeDir.Load(); v != nil {
		return v.(string)
	}
	return e.cfg.PublicDir
}

func (e *Engine) outputRoot() string {
	if e.outputDir != "" {
		return e.outputDir
	}
	return e.PublicDir()
}

func (e *Engine) LastBuildAt() time.Time {
	if unix := e.lastBuild.Load(); unix != 0 {
		return time.Unix(unix, 0)
	}
	return time.Time{}
}

func (e *Engine) NextPublishAt() time.Time {
	if unix := e.nextPublish.Load(); unix != 0 {
		return time.Unix(unix, 0)
	}
	return time.Time{}
}

func (e *Engine) LastBuildError() string { return e.lastErr.Load().(string) }

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
	root := e.outputRoot()
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

// writeFile writes one output file atomically and records it as current.
// Temp file + rename: pages are served straight off this directory with no
// coordination, so a truncate-then-write window would hand a concurrent
// request a truncated page (and a truncated /og/ PNG would be cached
// indefinitely by whoever fetched it in that window).
func (e *Engine) writeFile(path string, data []byte, written *pathSet) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	// Byte-identical output keeps the old mtime, so conditional requests
	// after a rebuild stay cheap 304s instead of full re-downloads.
	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, data) {
		written.add(path)
		return nil
	}
	if err := writeAtomic(path, data); err != nil {
		return err
	}
	written.add(path)
	return nil
}

// writeAtomic writes data to a temp file in the destination directory and
// renames it into place.
func writeAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name) // no-op once the rename succeeds
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(name, 0o644); err != nil {
		return err
	}
	return os.Rename(name, path)
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
	// RenderWith, not Render: the resolver is what turns each local image into
	// a set of renditions the browser can choose from. With no images directory
	// configured it is nil and every tag is left as written.
	htmlBody, err := markdown.RenderWith(a.Body, e.images.Resolve)
	if err != nil {
		return err
	}
	site := e.cfg.Site()
	ogImage, err := e.articleOGImage(a, site, written)
	if err != nil {
		return err
	}
	dir := filepath.Join(e.outputRoot(), "posts", a.Slug)
	data := e.pageVars(site, map[string]any{
		"Article": a,
		"HTML":    htmlBody,
		"URL":     baseURL(site) + "/posts/" + a.Slug,
		"OGImage": ogImage,
		// KaTeX is ~600 KB of script and fonts. Most posts have no math, so
		// the page only pulls it in when there is something to typeset.
		"HasMath": markdown.HasMath(htmlBody),
	})
	if err := e.renderPage("article", filepath.Join(dir, "index.html"), data, written); err != nil {
		return err
	}
	// markdown variant — copy source file (frontmatter + body)
	src, err := os.ReadFile(a.SourcePath)
	if err != nil {
		return err
	}
	if err := e.writeFile(filepath.Join(e.outputRoot(), "posts", a.Slug+".md"), src, written); err != nil {
		return err
	}
	agent := a.Agent
	if strings.TrimSpace(agent) == "" {
		agent = a.Description
	}
	agentDoc := fmt.Sprintf("# %s\n\n%s\n\nCanonical: %s/posts/%s\nRevision: %s\n", a.Title, agent, baseURL(site), a.Slug, a.Revision)
	return e.writeFile(filepath.Join(dir, "agent.md"), []byte(agentDoc), written)
}

// renderHome renders the front page: bio + a date-desc index of published
// articles.
func (e *Engine) renderHome(published []Article, written *pathSet) error {
	site := e.cfg.Site()
	ogImage, err := e.siteOGImage(site, written)
	if err != nil {
		return err
	}
	return e.renderPage("home", filepath.Join(e.outputRoot(), "index.html"), e.pageVars(site, map[string]any{
		"Articles": published,
		"OGImage":  ogImage,
	}), written)
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
	return e.renderPage("links", filepath.Join(e.outputRoot(), "links", "index.html"), e.pageVars(site, map[string]any{
		"Links":   links,
		"OGImage": ogImage,
	}), written)
}

// renderSearch renders the client-side search page. It is a static shell; the
// results come from /api/search at runtime.
func (e *Engine) renderSearch(written *pathSet) error {
	site := e.cfg.Site()
	ogImage, err := e.siteOGImage(site, written)
	if err != nil {
		return err
	}
	return e.renderPage("search", filepath.Join(e.outputRoot(), "search", "index.html"), e.pageVars(site, map[string]any{
		"OGImage": ogImage,
	}), written)
}

// render404 renders the not-found page the server returns for unmatched routes.
func (e *Engine) render404(written *pathSet) error {
	site := e.cfg.Site()
	ogImage, err := e.siteOGImage(site, written)
	if err != nil {
		return err
	}
	return e.renderPage("notfound", filepath.Join(e.outputRoot(), "404.html"), e.pageVars(site, map[string]any{
		"OGImage": ogImage,
	}), written)
}

// renderFeed renders the outbound RSS feed (feed.xml) of published articles.
func (e *Engine) renderFeed(published []Article, written *pathSet) error {
	site := e.cfg.Site()
	feedArts := make([]feeds.Article, 0, len(published))
	for _, a := range published {
		content, err := markdown.RenderWith(a.Body, e.images.Resolve)
		if err != nil {
			return err
		}
		feedArts = append(feedArts, feeds.Article{
			Title:       a.Title,
			Slug:        a.Slug,
			Date:        a.Date,
			Updated:     a.Updated,
			Description: a.Description,
			Content:     content,
			Tags:        a.Tags,
		})
	}
	out, err := feeds.RenderFeed(site, feedArts)
	if err != nil {
		return err
	}
	return e.writeFile(filepath.Join(e.outputRoot(), "feed.xml"), []byte(out), written)
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
		lastmod := a.Date
		if a.Updated.After(lastmod) {
			lastmod = a.Updated
		}
		addURL(base+"/posts/"+a.Slug, lastmod)
	}
	sb.WriteString("</urlset>\n")
	return e.writeFile(filepath.Join(e.outputRoot(), "sitemap.xml"), []byte(sb.String()), written)
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
	return e.writeFile(filepath.Join(e.outputRoot(), "robots.txt"), []byte(body), written)
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
