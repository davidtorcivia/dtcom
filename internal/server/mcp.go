package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"davidtorcivia.com/dtcom/internal/build"
	"davidtorcivia.com/dtcom/internal/siteconfig"
	"davidtorcivia.com/dtcom/internal/store"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// registerMCP wires the MCP-over-Streamable-HTTP endpoint at /mcp. Every tool
// exposes the same content-management operations as the REST API, calling the
// shared core methods on Deps so the two surfaces stay consistent.
//
// The server speaks the 2026-07-28 revision of the spec, which dropped the
// initialize handshake and protocol-level sessions: each request stands alone
// and carries its own protocol version in _meta. The SDK still answers older
// clients (back to 2024-11-05) on the same endpoint, so upgrading here did not
// cut anyone off.
func registerMCP(mux *http.ServeMux, d *Deps) {
	srv := mcp.NewServer(&mcp.Implementation{Name: "dtcom", Version: "1.0"}, &mcp.ServerOptions{
		Instructions: "Read and write the content of a single-author website: articles " +
			"(markdown posts), links, and site.yml configuration. Writes land on disk and " +
			"trigger a rebuild, so they are live immediately.",
	})
	registerArticleTools(srv, d)
	registerImageTools(srv, d)
	registerLinkTools(srv, d)
	registerSiteTools(srv, d)
	registerOpsTools(srv, d)
	registerArticleResources(srv, d)

	streamable := mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return srv },
		&mcp.StreamableHTTPOptions{
			// Nothing here is per-connection state — every tool reads and writes
			// the same files — so the sessionless mode the 2026-07-28 spec moved
			// to is what this server always wanted.
			Stateless: true,

			// The SDK refuses requests that arrive on a loopback listener bearing
			// a non-loopback Host, which is exactly the shape of a legitimate
			// request when a tunnel or reverse proxy fronts us on 127.0.0.1. Only
			// waive the check when the operator has said a proxy is really there;
			// bound straight to the network, the protection stays on.
			DisableLocalhostProtection: d.Cfg != nil && d.Cfg.TrustProxyHeaders,

			// Deliberately not setting PropagateRequestCancellation: a dropped
			// HTTP connection should not abort a half-finished write and rebuild.
		},
	)
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		// Same throttled bearer check the REST API uses, so the token can't be
		// guessed any faster here.
		if !d.authorizeBearer(w, r) {
			return
		}
		streamable.ServeHTTP(w, r)
	})
}

// ---------------------------------------------------------------------------
// Article resources
//
// The same posts the tools edit, offered as things a client can attach rather
// than call. A tool is a verb the model decides to use; a resource is a noun a
// person picks. Reaching for a post as context should not require the model to
// decide to go and fetch it.
//
// The list is rebuilt from disk whenever a client asks for it. Posts change
// through the tools here, through the admin UI, and by an editor writing a file
// that the watcher picks up — so there is no single hook to keep a static list
// in step with, and the honest answer is to read the directory when asked. That
// is the same cost list_articles already pays per call.
// ---------------------------------------------------------------------------

const articleURIPrefix = "dtcom://article/"

func articleURI(slug string) string { return articleURIPrefix + slug }

// articleSlugFromURI is the inverse, rejecting anything that is not one of ours
// or that could not name a post.
func articleSlugFromURI(uri string) (string, bool) {
	slug, ok := strings.CutPrefix(uri, articleURIPrefix)
	if !ok || !validSlug(slug) {
		return "", false
	}
	return slug, true
}

func registerArticleResources(srv *mcp.Server, d *Deps) {
	// The template is registered up front for two reasons: it tells a client it
	// may construct a URI for a post it knows the slug of without listing
	// first, and — because capabilities are inferred from the features present
	// — it is what makes the server advertise resources at all, before any post
	// has been listed.
	srv.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: articleURIPrefix + "{slug}",
		Name:        "article",
		Title:       "Article by slug",
		Description: "The markdown source of one post, frontmatter and all, as it is on disk.",
		MIMEType:    "text/markdown",
	}, d.readArticleResource)

	// Refresh the concrete list before answering a request that enumerates it.
	// Reads do not need this — they resolve from disk either way — so the cost
	// falls only on the call that actually wants a current list.
	srv.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method == "resources/list" {
				d.syncArticleResources(srv)
			}
			return next(ctx, method, req)
		}
	})
	d.syncArticleResources(srv)
}

// syncArticleResources makes the server's resource list match the posts on
// disk, adding what is new and withdrawing what is gone.
func (d *Deps) syncArticleResources(srv *mcp.Server) {
	arts, err := build.LoadArticles(d.postsDir())
	if err != nil {
		// A directory that cannot be read is not a reason to withdraw
		// everything already listed; leave the last good list in place.
		slog.Warn("mcp article resources", "err", err)
		return
	}

	present := make(map[string]bool, len(arts))
	for _, a := range arts {
		uri := articleURI(a.Slug)
		present[uri] = true
		title := a.Title
		if a.Draft {
			// Drafts are listed — they are the posts most likely to be worked
			// on — but never silently, since they are not on the site yet.
			title += " (draft)"
		}
		srv.AddResource(&mcp.Resource{
			URI:         uri,
			Name:        a.Slug,
			Title:       title,
			Description: a.Description,
			MIMEType:    "text/markdown",
		}, d.readArticleResource)
	}

	d.mcpResMu.Lock()
	var gone []string
	for uri := range d.mcpArticleRes {
		if !present[uri] {
			gone = append(gone, uri)
		}
	}
	d.mcpArticleRes = present
	d.mcpResMu.Unlock()

	if len(gone) > 0 {
		srv.RemoveResources(gone...)
	}
}

// readArticleResource serves one post's markdown source.
//
// The source file verbatim, which is what /posts/<slug>.md serves too: the
// frontmatter is the post's own metadata and dropping it would lose the date
// and tags for the sake of tidiness.
func (d *Deps) readArticleResource(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	uri := req.Params.URI
	slug, ok := articleSlugFromURI(uri)
	if !ok {
		return nil, mcp.ResourceNotFoundError(uri)
	}
	a, err := d.findArticleBySlug(slug)
	if err != nil {
		return nil, err
	}
	if a == nil {
		return nil, mcp.ResourceNotFoundError(uri)
	}
	source, err := os.ReadFile(a.SourcePath)
	if err != nil {
		return nil, err
	}
	return &mcp.ReadResourceResult{
		Contents: []*mcp.ResourceContents{{
			URI:      uri,
			MIMEType: "text/markdown",
			Text:     string(source),
		}},
	}, nil
}

// Tool annotations: what a tool does to the site. destructiveHint and
// openWorldHint both default to true when absent, so an unannotated server
// advertises every tool as capable of wrecking something — these make
// list_articles distinguishable from delete_article.

func boolp(b bool) *bool { return &b }

// reads annotates a tool that only looks at the site.
func reads(title string) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{
		Title:         title,
		ReadOnlyHint:  true,
		OpenWorldHint: boolp(false),
	}
}

// writes annotates a tool that changes the site without taking anything away.
// Idempotent means calling it twice with the same arguments leaves the site in
// the same state as calling it once — true of the update_* tools, which
// replace a value, and false of create_article and add_link, which add one.
func writes(title string, idempotent bool) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{
		Title:           title,
		DestructiveHint: boolp(false),
		IdempotentHint:  idempotent,
		OpenWorldHint:   boolp(false),
	}
}

// destroys annotates a tool that removes something. Idempotent by nature: a
// second delete of the same slug finds nothing left to do. That is not a
// reason to skip the confirmation — the first call is the irreversible one.
func destroys(title string) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{
		Title:           title,
		DestructiveHint: boolp(true),
		IdempotentHint:  true,
		OpenWorldHint:   boolp(false),
	}
}

// fetches annotates a tool that contacts somebody else's server.
func fetches(title string) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{
		Title:           title,
		DestructiveHint: boolp(false),
		OpenWorldHint:   boolp(true),
	}
}

// ---------------------------------------------------------------------------
// Tool results
//
// Every tool declares the shape of what it returns, so tools/list carries an
// output schema and each answer comes back as structuredContent validated
// against it rather than as a wall of JSON a client has to parse out of a text
// block. The SDK still puts the serialised JSON in a text block alongside, for
// clients that predate structured content.
//
// The json tags are load-bearing: they keep the wire names these tools already
// answered with, from back when each result was assembled as a map literal.
// ---------------------------------------------------------------------------

// articleDetail is one article, whole.
type articleDetail struct {
	Slug        string   `json:"slug"`
	Title       string   `json:"title"`
	Date        string   `json:"date"`
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
	Draft       bool     `json:"draft"`
	Body        string   `json:"body"`
}

// articleResult is what the tools that change an article answer with.
type articleResult struct {
	Slug   string `json:"slug"`
	Status string `json:"status"`
}

type linkAddedResult struct {
	ID int64 `json:"id"`
}

type linkRemovedResult struct {
	ID     int64  `json:"id"`
	Status string `json:"status"`
}

type sectionResult struct {
	Section string `json:"section"`
	Status  string `json:"status"`
}

type statusResult struct {
	Status string `json:"status"`
}

type importedResult struct {
	Imported int `json:"imported"`
}

// ---------------------------------------------------------------------------
// Article tools
// ---------------------------------------------------------------------------

type noArgs struct{}

type getArticleArgs struct {
	Slug string `json:"slug" jsonschema:"Article slug, e.g. \"schedlock\"."`
}

type createArticleArgs struct {
	Title       string   `json:"title" jsonschema:"Article title."`
	Body        string   `json:"body" jsonschema:"Markdown body."`
	Date        string   `json:"date,omitempty" jsonschema:"YYYY-MM-DD. Defaults to today."`
	Description string   `json:"description,omitempty" jsonschema:"Short description / summary."`
	Tags        []string `json:"tags,omitempty" jsonschema:"Tag list."`
	Slug        string   `json:"slug,omitempty" jsonschema:"Override slug. Derived from title if omitted."`
	Draft       bool     `json:"draft,omitempty" jsonschema:"Save as a draft (excluded from build)."`
}

// updateArticleArgs uses pointers for the fields that may legitimately be set
// to an empty value. Presence in the call — not emptiness — decides whether a
// field changes, so a caller can genuinely clear a description or empty a tag
// list. (Treating "" as "unchanged", as an earlier version did, made those
// edits impossible.)
type updateArticleArgs struct {
	Slug        string    `json:"slug" jsonschema:"Slug of the article to update."`
	Title       *string   `json:"title,omitempty" jsonschema:"New title."`
	Body        *string   `json:"body,omitempty" jsonschema:"New markdown body."`
	Date        string    `json:"date,omitempty" jsonschema:"New date (YYYY-MM-DD). Defaults to the original."`
	Description *string   `json:"description,omitempty" jsonschema:"New description."`
	Tags        *[]string `json:"tags,omitempty" jsonschema:"New tag list."`
	Draft       *bool     `json:"draft,omitempty" jsonschema:"Toggle draft status."`
}

type searchArticlesArgs struct {
	Query string `json:"query" jsonschema:"Search query."`
}

// patchArticleArgs is an edit that names only what changes. update_article
// carries the whole body, which for a long post is tens of kilobytes on the
// wire for a one-line correction — and the client relays in front of this
// server are where those payloads go to die.
type patchArticleArgs struct {
	Slug    string `json:"slug" jsonschema:"Slug of the article to patch."`
	Find    string `json:"find" jsonschema:"Exact text to find in the body. Must match exactly once unless all is set."`
	Replace string `json:"replace" jsonschema:"Text to put in its place. Empty deletes the matched text."`
	All     bool   `json:"all,omitempty" jsonschema:"Replace every occurrence instead of requiring exactly one."`
}

// patchResult reports how many places actually changed, which is the only way
// a caller of the all form learns whether it hit one line or forty.
type patchResult struct {
	Slug         string `json:"slug"`
	Status       string `json:"status"`
	Replacements int    `json:"replacements"`
}

func registerArticleTools(srv *mcp.Server, d *Deps) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_articles",
		Annotations: reads("List articles"),
		Description: "List all articles with slug, title, date, draft status, and description.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, _ noArgs) (*mcp.CallToolResult, articleListResult, error) {
		arts, err := build.LoadArticles(d.postsDir())
		if err != nil {
			return nil, articleListResult{}, err
		}
		return nil, articleListResult{Articles: artSummaries(arts)}, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_article",
		Annotations: reads("Read an article"),
		Description: "Get a single article by slug: frontmatter + markdown body." + figureConventions,
	}, func(ctx context.Context, req *mcp.CallToolRequest, args getArticleArgs) (*mcp.CallToolResult, articleDetail, error) {
		a, err := d.findArticleBySlug(args.Slug)
		if err != nil {
			return nil, articleDetail{}, err
		}
		if a == nil {
			return nil, articleDetail{}, fmt.Errorf("article %q not found", args.Slug)
		}
		return nil, articleDetail{
			Slug: a.Slug, Title: a.Title, Date: a.Date.Format("2006-01-02"),
			Description: a.Description, Tags: a.Tags, Draft: a.Draft, Body: a.Body,
		}, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "create_article",
		Annotations: writes("Write a new article", false),
		Description: "Create a new article. Writes content/posts/<date>-<slug>.md and rebuilds." + figureConventions,
	}, func(ctx context.Context, req *mcp.CallToolRequest, args createArticleArgs) (*mcp.CallToolResult, articleResult, error) {
		slug, status, err := d.createArticle(articleInput{
			Title:       args.Title,
			Slug:        args.Slug,
			Date:        args.Date,
			Description: args.Description,
			Tags:        args.Tags,
			Body:        args.Body,
			Draft:       args.Draft,
		})
		if err != nil {
			return nil, articleResult{}, fmt.Errorf("create failed (%d): %w", status, err)
		}
		return nil, articleResult{Slug: slug, Status: "created"}, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "update_article",
		Annotations: writes("Edit an article", true),
		Description: "Update an existing article identified by slug. Any field may be omitted to keep the current value." + figureConventions,
	}, func(ctx context.Context, req *mcp.CallToolRequest, args updateArticleArgs) (*mcp.CallToolResult, articleResult, error) {
		a, err := d.findArticleBySlug(args.Slug)
		if err != nil {
			return nil, articleResult{}, err
		}
		if a == nil {
			return nil, articleResult{}, fmt.Errorf("article %q not found", args.Slug)
		}
		status, err := d.updateArticle(args.Slug, articleInput{
			Title:       orKeep(args.Title, a.Title),
			Date:        args.Date,
			Description: orKeep(args.Description, a.Description),
			Tags:        orKeep(args.Tags, a.Tags),
			Body:        orKeep(args.Body, a.Body),
			Draft:       orKeep(args.Draft, a.Draft),
		})
		if err != nil {
			return nil, articleResult{}, fmt.Errorf("update failed (%d): %w", status, err)
		}
		return nil, articleResult{Slug: args.Slug, Status: "updated"}, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "patch_article",
		Annotations: writes("Patch an article", false),
		Description: "Replace one exact passage inside an article's body, leaving everything else alone. " +
			"Prefer this over update_article for edits to an existing post: it sends only the text that " +
			"changes rather than the whole body. find must match exactly once, or the call fails and " +
			"nothing is written — pass all to replace every occurrence instead." + figureConventions,
	}, func(ctx context.Context, req *mcp.CallToolRequest, args patchArticleArgs) (*mcp.CallToolResult, patchResult, error) {
		if args.Find == "" {
			return nil, patchResult{}, errors.New("find is required")
		}
		a, err := d.findArticleBySlug(args.Slug)
		if err != nil {
			return nil, patchResult{}, err
		}
		if a == nil {
			return nil, patchResult{}, fmt.Errorf("article %q not found", args.Slug)
		}
		// Refusing an ambiguous match is the whole safety story here: a body
		// this caller cannot see, edited by a substring it guessed at, is
		// exactly where a blind replace-all quietly mangles a post.
		n := strings.Count(a.Body, args.Find)
		switch {
		case n == 0:
			return nil, patchResult{}, fmt.Errorf("find text does not appear in %q", args.Slug)
		case n > 1 && !args.All:
			return nil, patchResult{}, fmt.Errorf(
				"find text appears %d times in %q; give more surrounding text to pin one, or set all", n, args.Slug)
		}
		status, err := d.updateArticle(args.Slug, articleInput{
			Title:       a.Title,
			Description: a.Description,
			Tags:        a.Tags,
			Body:        strings.ReplaceAll(a.Body, args.Find, args.Replace),
			Draft:       a.Draft,
		})
		if err != nil {
			return nil, patchResult{}, fmt.Errorf("patch failed (%d): %w", status, err)
		}
		return nil, patchResult{Slug: args.Slug, Status: "patched", Replacements: n}, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "delete_article",
		Annotations: destroys("Delete an article"),
		Description: "Delete an article by slug. Removes the .md file and rebuilds.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args getArticleArgs) (*mcp.CallToolResult, articleResult, error) {
		status, err := d.deleteArticle(args.Slug)
		if err != nil {
			return nil, articleResult{}, fmt.Errorf("delete failed (%d): %w", status, err)
		}
		return nil, articleResult{Slug: args.Slug, Status: "deleted"}, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "search_articles",
		Annotations: reads("Search articles"),
		Description: "Full-text search across article titles, bodies, and tags.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args searchArticlesArgs) (*mcp.CallToolResult, searchResult, error) {
		hits, err := d.Store.SearchArticles(args.Query, 20)
		if err != nil {
			return nil, searchResult{}, err
		}
		if hits == nil {
			hits = []store.SearchHit{}
		}
		return nil, searchResult{Results: hits}, nil
	})
}

// ---------------------------------------------------------------------------
// Link tools
// ---------------------------------------------------------------------------

type addLinkArgs struct {
	Label string `json:"label" jsonschema:"Link label / title."`
	Href  string `json:"href" jsonschema:"Link URL."`
	Note  string `json:"note,omitempty" jsonschema:"Optional note."`
}

type removeLinkArgs struct {
	ID int64 `json:"id" jsonschema:"Link id from list_links."`
}

func registerLinkTools(srv *mcp.Server, d *Deps) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_links",
		Annotations: reads("List links"),
		Description: "List all links (manual + RSS-imported), newest first.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, _ noArgs) (*mcp.CallToolResult, linkListResult, error) {
		links, err := d.Store.ListLinks(500)
		if err != nil {
			return nil, linkListResult{}, err
		}
		if links == nil {
			links = []store.Link{}
		}
		return nil, linkListResult{Links: links}, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "add_link",
		Annotations: writes("Add a link", false),
		Description: "Add a manual link and rebuild.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args addLinkArgs) (*mcp.CallToolResult, linkAddedResult, error) {
		if args.Label == "" || args.Href == "" {
			return nil, linkAddedResult{}, errors.New("label and href required")
		}
		id, err := d.Store.AddLink(store.Link{
			Label: args.Label, Href: args.Href, Note: args.Note,
			Source: "manual", SortDate: time.Now().Unix(),
		})
		if err != nil {
			switch {
			case errors.Is(err, store.ErrDisallowedScheme):
				return nil, linkAddedResult{}, errors.New("href must use http://, https://, or mailto:")
			case errors.Is(err, store.ErrDuplicateLink):
				return nil, linkAddedResult{}, fmt.Errorf("a link with href %q already exists", args.Href)
			}
			return nil, linkAddedResult{}, err
		}
		if err := d.Engine.Rebuild(); err != nil {
			return nil, linkAddedResult{}, err
		}
		return nil, linkAddedResult{ID: id}, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "remove_link",
		Annotations: destroys("Remove a link"),
		Description: "Remove a manual link by id.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args removeLinkArgs) (*mcp.CallToolResult, linkRemovedResult, error) {
		if args.ID <= 0 {
			return nil, linkRemovedResult{}, errors.New("invalid id")
		}
		removed, err := d.Store.RemoveLink(args.ID)
		if err != nil {
			return nil, linkRemovedResult{}, err
		}
		if !removed {
			return nil, linkRemovedResult{}, fmt.Errorf(
				"no manual link with id %d (RSS-imported links can't be removed; the next poll would re-import them)", args.ID)
		}
		if err := d.Engine.Rebuild(); err != nil {
			return nil, linkRemovedResult{}, err
		}
		return nil, linkRemovedResult{ID: args.ID, Status: "removed"}, nil
	})
}

// ---------------------------------------------------------------------------
// Site config tools
// ---------------------------------------------------------------------------

// siteSectionArgs carries one site.yml section verbatim. The element shape
// differs per section (strings for bio, objects for the rest), so the schema
// stays deliberately open and the value is re-marshalled straight through to
// the same core the REST API uses.
type siteSectionArgs struct {
	Value []any `json:"value" jsonschema:"New value for the section."`
}

func registerSiteTools(srv *mcp.Server, d *Deps) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_site",
		Annotations: reads("Read site config"),
		Description: "Return the full site.yml config (title, author, bio, nav, social, rss_feeds, footer_left).",
	}, func(ctx context.Context, req *mcp.CallToolRequest, _ noArgs) (*mcp.CallToolResult, *siteconfig.Config, error) {
		return nil, d.Site(), nil
	})

	// update_bio / update_nav / update_social / update_rss_feeds each take a
	// single JSON array argument matching the corresponding site.yml section.
	// They route through the same updateSiteSection core used by the REST API.
	for _, def := range []struct {
		name, section, title, desc string
	}{
		{"update_bio", "bio", "Replace the bio", "Replace the bio paragraphs (array of strings)."},
		{"update_nav", "nav", "Replace the nav links", "Replace the nav links (array of {label, href})."},
		{"update_social", "social", "Replace the social links", "Replace the social links (array of {label, href, icon})."},
		{"update_rss_feeds", "rss_feeds", "Replace the RSS feeds", "Replace the inbound RSS feeds (array of {url, label, enabled})."},
	} {
		section := def.section
		mcp.AddTool(srv, &mcp.Tool{
			Name:        def.name,
			Description: def.desc + " Saves site.yml and rebuilds.",
			Annotations: writes(def.title, true),
		}, func(ctx context.Context, req *mcp.CallToolRequest, args siteSectionArgs) (*mcp.CallToolResult, sectionResult, error) {
			valueBytes, err := json.Marshal(args.Value)
			if err != nil {
				return nil, sectionResult{}, err
			}
			if err := d.updateSiteSection(section, strings.NewReader(string(valueBytes))); err != nil {
				return nil, sectionResult{}, fmt.Errorf("update %s failed (%d): %w", section, httpToStatus(err), err)
			}
			return nil, sectionResult{Section: section, Status: "updated"}, nil
		})
	}
}

// ---------------------------------------------------------------------------
// Ops tools
// ---------------------------------------------------------------------------

func registerOpsTools(srv *mcp.Server, d *Deps) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "regenerate",
		Annotations: writes("Rebuild the site", true),
		Description: "Force a full site rebuild (re-renders all pages, feeds, sitemap, and the search index).",
	}, func(ctx context.Context, req *mcp.CallToolRequest, _ noArgs) (*mcp.CallToolResult, statusResult, error) {
		if err := d.Engine.Rebuild(); err != nil {
			return nil, statusResult{}, err
		}
		return nil, statusResult{Status: "ok"}, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_stats",
		Annotations: reads("Read view stats"),
		Description: "Return page-view stats: total, per-path, and per-day (30d).",
	}, func(ctx context.Context, req *mcp.CallToolRequest, _ noArgs) (*mcp.CallToolResult, *store.Stats, error) {
		s, err := d.Store.Stats()
		if err != nil {
			return nil, nil, err
		}
		return nil, s, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "refresh_feeds",
		Annotations: fetches("Poll RSS feeds"),
		Description: "Poll all enabled RSS feeds and import new items.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, _ noArgs) (*mcp.CallToolResult, importedResult, error) {
		n := d.Poller.Poll(ctx, d.Site())
		return nil, importedResult{Imported: n}, nil
	})
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// The list-shaped results are wrapped in an object rather than returned as a
// bare slice, because MCP's structuredContent is defined as an object and a
// tool whose outputSchema is a top-level array is rejected outright: Claude
// Desktop reports "Invalid result for tools/list" and refuses to load the whole
// server, naming the offending tool by index. The Go SDK derives the schema
// from the handler's return type, so the shape has to be fixed here.
//
// The slices are always allocated, never nil, so an empty result serializes as
// [] rather than null and matches the array the schema promises.

type articleListResult struct {
	Articles []articleSummary `json:"articles" jsonschema:"Every article, newest first."`
}

type searchResult struct {
	Results []store.SearchHit `json:"results" jsonschema:"Matching articles, best match first."`
}

type linkListResult struct {
	Links []store.Link `json:"links" jsonschema:"Every link, newest first."`
}

// artSummaries projects articles to the same shape the REST list endpoint
// emits, so the two surfaces agree.
func artSummaries(arts []build.Article) []articleSummary {
	out := make([]articleSummary, 0, len(arts))
	for _, a := range arts {
		out = append(out, articleSummary{
			Slug: a.Slug, Title: a.Title, Date: a.Date.Format("2006-01-02"),
			Description: a.Description, Draft: a.Draft,
		})
	}
	return out
}

// orKeep returns the caller-supplied value, or fallback when the field was not
// supplied at all.
func orKeep[T any](p *T, fallback T) T {
	if p == nil {
		return fallback
	}
	return *p
}
