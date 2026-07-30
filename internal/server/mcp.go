package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"davidtorcivia.com/dtcom/internal/build"
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
	registerLinkTools(srv, d)
	registerSiteTools(srv, d)
	registerOpsTools(srv, d)

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

func registerArticleTools(srv *mcp.Server, d *Deps) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_articles",
		Description: "List all articles with slug, title, date, draft status, and description.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, _ noArgs) (*mcp.CallToolResult, any, error) {
		arts, err := build.LoadArticles(d.postsDir())
		if err != nil {
			return nil, nil, err
		}
		return jsonResult(artSummaries(arts))
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_article",
		Description: "Get a single article by slug: frontmatter + markdown body.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args getArticleArgs) (*mcp.CallToolResult, any, error) {
		a, err := d.findArticleBySlug(args.Slug)
		if err != nil {
			return nil, nil, err
		}
		if a == nil {
			return nil, nil, fmt.Errorf("article %q not found", args.Slug)
		}
		return jsonResult(map[string]any{
			"slug": a.Slug, "title": a.Title, "date": a.Date.Format("2006-01-02"),
			"description": a.Description, "tags": a.Tags, "draft": a.Draft, "body": a.Body,
		})
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "create_article",
		Description: "Create a new article. Writes content/posts/<date>-<slug>.md and rebuilds.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args createArticleArgs) (*mcp.CallToolResult, any, error) {
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
			return nil, nil, fmt.Errorf("create failed (%d): %w", status, err)
		}
		return jsonResult(map[string]any{"slug": slug, "status": "created"})
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "update_article",
		Description: "Update an existing article identified by slug. Any field may be omitted to keep the current value.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args updateArticleArgs) (*mcp.CallToolResult, any, error) {
		a, err := d.findArticleBySlug(args.Slug)
		if err != nil {
			return nil, nil, err
		}
		if a == nil {
			return nil, nil, fmt.Errorf("article %q not found", args.Slug)
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
			return nil, nil, fmt.Errorf("update failed (%d): %w", status, err)
		}
		return jsonResult(map[string]any{"slug": args.Slug, "status": "updated"})
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "delete_article",
		Description: "Delete an article by slug. Removes the .md file and rebuilds.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args getArticleArgs) (*mcp.CallToolResult, any, error) {
		status, err := d.deleteArticle(args.Slug)
		if err != nil {
			return nil, nil, fmt.Errorf("delete failed (%d): %w", status, err)
		}
		return jsonResult(map[string]any{"slug": args.Slug, "status": "deleted"})
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "search_articles",
		Description: "Full-text search across article titles, bodies, and tags.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args searchArticlesArgs) (*mcp.CallToolResult, any, error) {
		hits, err := d.Store.SearchArticles(args.Query, 20)
		if err != nil {
			return nil, nil, err
		}
		return jsonResult(hits)
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
		Description: "List all links (manual + RSS-imported), newest first.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, _ noArgs) (*mcp.CallToolResult, any, error) {
		links, err := d.Store.ListLinks(500)
		if err != nil {
			return nil, nil, err
		}
		return jsonResult(links)
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "add_link",
		Description: "Add a manual link and rebuild.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args addLinkArgs) (*mcp.CallToolResult, any, error) {
		if args.Label == "" || args.Href == "" {
			return nil, nil, errors.New("label and href required")
		}
		id, err := d.Store.AddLink(store.Link{
			Label: args.Label, Href: args.Href, Note: args.Note,
			Source: "manual", SortDate: time.Now().Unix(),
		})
		if err != nil {
			switch {
			case errors.Is(err, store.ErrDisallowedScheme):
				return nil, nil, errors.New("href must use http://, https://, or mailto:")
			case errors.Is(err, store.ErrDuplicateLink):
				return nil, nil, fmt.Errorf("a link with href %q already exists", args.Href)
			}
			return nil, nil, err
		}
		if err := d.Engine.Rebuild(); err != nil {
			return nil, nil, err
		}
		return jsonResult(map[string]int64{"id": id})
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "remove_link",
		Description: "Remove a manual link by id.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args removeLinkArgs) (*mcp.CallToolResult, any, error) {
		if args.ID <= 0 {
			return nil, nil, errors.New("invalid id")
		}
		removed, err := d.Store.RemoveLink(args.ID)
		if err != nil {
			return nil, nil, err
		}
		if !removed {
			return nil, nil, fmt.Errorf(
				"no manual link with id %d (RSS-imported links can't be removed; the next poll would re-import them)", args.ID)
		}
		if err := d.Engine.Rebuild(); err != nil {
			return nil, nil, err
		}
		return jsonResult(map[string]any{"id": args.ID, "status": "removed"})
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
		Description: "Return the full site.yml config (title, author, bio, nav, social, rss_feeds, footer_left).",
	}, func(ctx context.Context, req *mcp.CallToolRequest, _ noArgs) (*mcp.CallToolResult, any, error) {
		return jsonResult(d.Site())
	})

	// update_bio / update_nav / update_social / update_rss_feeds each take a
	// single JSON array argument matching the corresponding site.yml section.
	// They route through the same updateSiteSection core used by the REST API.
	for _, def := range []struct {
		name, section, desc string
	}{
		{"update_bio", "bio", "Replace the bio paragraphs (array of strings)."},
		{"update_nav", "nav", "Replace the nav links (array of {label, href})."},
		{"update_social", "social", "Replace the social links (array of {label, href, icon})."},
		{"update_rss_feeds", "rss_feeds", "Replace the inbound RSS feeds (array of {url, label, enabled})."},
	} {
		section := def.section
		mcp.AddTool(srv, &mcp.Tool{
			Name:        def.name,
			Description: def.desc + " Saves site.yml and rebuilds.",
		}, func(ctx context.Context, req *mcp.CallToolRequest, args siteSectionArgs) (*mcp.CallToolResult, any, error) {
			valueBytes, err := json.Marshal(args.Value)
			if err != nil {
				return nil, nil, err
			}
			if err := d.updateSiteSection(section, strings.NewReader(string(valueBytes))); err != nil {
				return nil, nil, fmt.Errorf("update %s failed (%d): %w", section, httpToStatus(err), err)
			}
			return jsonResult(map[string]string{"section": section, "status": "updated"})
		})
	}
}

// ---------------------------------------------------------------------------
// Ops tools
// ---------------------------------------------------------------------------

func registerOpsTools(srv *mcp.Server, d *Deps) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "regenerate",
		Description: "Force a full site rebuild (re-renders all pages, feeds, sitemap, and the search index).",
	}, func(ctx context.Context, req *mcp.CallToolRequest, _ noArgs) (*mcp.CallToolResult, any, error) {
		if err := d.Engine.Rebuild(); err != nil {
			return nil, nil, err
		}
		return jsonResult(map[string]string{"status": "ok"})
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_stats",
		Description: "Return page-view stats: total, per-path, and per-day (30d).",
	}, func(ctx context.Context, req *mcp.CallToolRequest, _ noArgs) (*mcp.CallToolResult, any, error) {
		s, err := d.Store.Stats()
		if err != nil {
			return nil, nil, err
		}
		return jsonResult(s)
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "refresh_feeds",
		Description: "Poll all enabled RSS feeds and import new items.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, _ noArgs) (*mcp.CallToolResult, any, error) {
		n := d.Poller.Poll(ctx, d.Site())
		return jsonResult(map[string]int{"imported": n})
	})
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// artSummaries projects articles to the same shape the REST list endpoint
// emits, so the two surfaces agree.
func artSummaries(arts []build.Article) []articleSummary {
	out := make([]articleSummary, 0, len(arts))
	for _, a := range arts {
		out = append(out, articleSummary{
			Slug: a.Slug, Title: a.Title, Date: a.Date.Format("2006-01-02"), Draft: a.Draft,
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

// jsonResult packs v as the tool's text content, in the handler's return
// shape. Every tool answers with pretty-printed JSON.
func jsonResult(v any) (*mcp.CallToolResult, any, error) {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: jsonMust(v)}},
	}, nil, nil
}

// jsonMust encodes v; a marshal failure is a programmer error and panics.
func jsonMust(v any) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		panic(fmt.Sprintf("mcp json marshal: %v", err))
	}
	return string(b)
}
