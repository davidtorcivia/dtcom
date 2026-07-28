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

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

// registerMCP wires the MCP-over-Streamable-HTTP endpoint at /mcp. Every tool
// exposes the same content-management operations as the REST API, calling the
// shared core methods on Deps so the two surfaces stay consistent.
func registerMCP(mux *http.ServeMux, d *Deps) {
	srv := mcpserver.NewMCPServer("dtcom", "1.0")
	registerArticleTools(srv, d)
	registerLinkTools(srv, d)
	registerSiteTools(srv, d)
	registerOpsTools(srv, d)

	streamable := mcpserver.NewStreamableHTTPServer(srv)
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

func registerArticleTools(srv *mcpserver.MCPServer, d *Deps) {
	srv.AddTool(
		mcp.NewTool("list_articles",
			mcp.WithDescription("List all articles with slug, title, date, draft status, and description."),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			arts, err := build.LoadArticles(d.postsDir())
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return mcp.NewToolResultText(jsonMust(artSummaries(arts))), nil
		},
	)

	srv.AddTool(
		mcp.NewTool("get_article",
			mcp.WithDescription("Get a single article by slug: frontmatter + markdown body."),
			mcp.WithString("slug", mcp.Required(), mcp.Description("Article slug, e.g. \"schedlock\".")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			slug := req.GetString("slug", "")
			a, err := d.findArticleBySlug(slug)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			if a == nil {
				return mcp.NewToolResultError(fmt.Sprintf("article %q not found", slug)), nil
			}
			return mcp.NewToolResultText(jsonMust(map[string]any{
				"slug": a.Slug, "title": a.Title, "date": a.Date.Format("2006-01-02"),
				"description": a.Description, "tags": a.Tags, "draft": a.Draft, "body": a.Body,
			})), nil
		},
	)

	srv.AddTool(
		mcp.NewTool("create_article",
			mcp.WithDescription("Create a new article. Writes content/posts/<date>-<slug>.md and rebuilds."),
			mcp.WithString("title", mcp.Required(), mcp.Description("Article title.")),
			mcp.WithString("body", mcp.Required(), mcp.Description("Markdown body.")),
			mcp.WithString("date", mcp.Description("YYYY-MM-DD. Defaults to today.")),
			mcp.WithString("description", mcp.Description("Short description / summary.")),
			mcp.WithArray("tags", mcp.WithStringItems(), mcp.Description("Tag list.")),
			mcp.WithString("slug", mcp.Description("Override slug. Derived from title if omitted.")),
			mcp.WithBoolean("draft", mcp.Description("Save as a draft (excluded from build).")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			in := articleInput{
				Title:       req.GetString("title", ""),
				Slug:        req.GetString("slug", ""),
				Date:        req.GetString("date", ""),
				Description: req.GetString("description", ""),
				Tags:        req.GetStringSlice("tags", nil),
				Body:        req.GetString("body", ""),
				Draft:       req.GetBool("draft", false),
			}
			slug, status, err := d.createArticle(in)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("create failed (%d): %s", status, err)), nil
			}
			return mcp.NewToolResultText(jsonMust(map[string]any{"slug": slug, "status": "created"})), nil
		},
	)

	srv.AddTool(
		mcp.NewTool("update_article",
			mcp.WithDescription("Update an existing article identified by slug. Any field may be omitted to keep the current value."),
			mcp.WithString("slug", mcp.Required(), mcp.Description("Slug of the article to update.")),
			mcp.WithString("title", mcp.Description("New title.")),
			mcp.WithString("body", mcp.Description("New markdown body.")),
			mcp.WithString("date", mcp.Description("New date (YYYY-MM-DD). Defaults to original.")),
			mcp.WithString("description", mcp.Description("New description.")),
			mcp.WithArray("tags", mcp.WithStringItems(), mcp.Description("New tag list.")),
			mcp.WithBoolean("draft", mcp.Description("Toggle draft status.")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			slug := req.GetString("slug", "")
			a, err := d.findArticleBySlug(slug)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			if a == nil {
				return mcp.NewToolResultError(fmt.Sprintf("article %q not found", slug)), nil
			}
			// Build the input preserving existing fields unless overridden.
			// Presence in the raw arguments — not emptiness — decides whether
			// a field changes, so a caller can genuinely clear a description
			// or empty a tag list. (Treating "" as "unchanged", as an earlier
			// version did, made those edits impossible.)
			args := rawArgs(req)
			in := articleInput{
				Title:       keepString(args, "title", a.Title),
				Date:        req.GetString("date", ""),
				Description: keepString(args, "description", a.Description),
				Tags:        keepStrings(args, "tags", a.Tags),
				Body:        keepString(args, "body", a.Body),
				Draft:       req.GetBool("draft", a.Draft),
			}
			status, err := d.updateArticle(slug, in)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("update failed (%d): %s", status, err)), nil
			}
			return mcp.NewToolResultText(jsonMust(map[string]any{"slug": slug, "status": "updated"})), nil
		},
	)

	srv.AddTool(
		mcp.NewTool("delete_article",
			mcp.WithDescription("Delete an article by slug. Removes the .md file and rebuilds."),
			mcp.WithString("slug", mcp.Required(), mcp.Description("Slug of the article to delete.")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			slug := req.GetString("slug", "")
			status, err := d.deleteArticle(slug)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("delete failed (%d): %s", status, err)), nil
			}
			return mcp.NewToolResultText(jsonMust(map[string]any{"slug": slug, "status": "deleted"})), nil
		},
	)

	srv.AddTool(
		mcp.NewTool("search_articles",
			mcp.WithDescription("Full-text search across article titles, bodies, and tags."),
			mcp.WithString("query", mcp.Required(), mcp.Description("Search query.")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			q := req.GetString("query", "")
			hits, err := d.Store.SearchArticles(q, 20)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return mcp.NewToolResultText(jsonMust(hits)), nil
		},
	)
}

// ---------------------------------------------------------------------------
// Link tools
// ---------------------------------------------------------------------------

func registerLinkTools(srv *mcpserver.MCPServer, d *Deps) {
	srv.AddTool(
		mcp.NewTool("list_links",
			mcp.WithDescription("List all links (manual + RSS-imported), newest first."),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			links, err := d.Store.ListLinks(500)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return mcp.NewToolResultText(jsonMust(links)), nil
		},
	)

	srv.AddTool(
		mcp.NewTool("add_link",
			mcp.WithDescription("Add a manual link and rebuild."),
			mcp.WithString("label", mcp.Required(), mcp.Description("Link label / title.")),
			mcp.WithString("href", mcp.Required(), mcp.Description("Link URL.")),
			mcp.WithString("note", mcp.Description("Optional note.")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			label := req.GetString("label", "")
			href := req.GetString("href", "")
			if label == "" || href == "" {
				return mcp.NewToolResultError("label and href required"), nil
			}
			id, err := d.Store.AddLink(store.Link{
				Label: label, Href: href, Note: req.GetString("note", ""),
				Source: "manual", SortDate: time.Now().Unix(),
			})
			if err != nil {
				switch {
				case errors.Is(err, store.ErrDisallowedScheme):
					return mcp.NewToolResultError("href must use http://, https://, or mailto:"), nil
				case errors.Is(err, store.ErrDuplicateLink):
					return mcp.NewToolResultError(fmt.Sprintf("a link with href %q already exists", href)), nil
				}
				return mcp.NewToolResultError(err.Error()), nil
			}
			if err := d.Engine.Rebuild(); err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return mcp.NewToolResultText(jsonMust(map[string]int64{"id": id})), nil
		},
	)

	srv.AddTool(
		mcp.NewTool("remove_link",
			mcp.WithDescription("Remove a manual link by id."),
			mcp.WithNumber("id", mcp.Required(), mcp.Description("Link id from list_links.")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			id := req.GetInt("id", 0)
			if id <= 0 {
				return mcp.NewToolResultError("invalid id"), nil
			}
			removed, err := d.Store.RemoveLink(int64(id))
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			if !removed {
				return mcp.NewToolResultError(fmt.Sprintf(
					"no manual link with id %d (RSS-imported links can't be removed; the next poll would re-import them)", id)), nil
			}
			if err := d.Engine.Rebuild(); err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return mcp.NewToolResultText(jsonMust(map[string]any{"id": id, "status": "removed"})), nil
		},
	)
}

// ---------------------------------------------------------------------------
// Site config tools
// ---------------------------------------------------------------------------

func registerSiteTools(srv *mcpserver.MCPServer, d *Deps) {
	srv.AddTool(
		mcp.NewTool("get_site",
			mcp.WithDescription("Return the full site.yml config (title, author, bio, nav, social, rss_feeds, footer_left)."),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText(jsonMust(d.Site())), nil
		},
	)

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
		// Capture loop variables for the closure.
		name, section, desc := def.name, def.section, def.desc
		srv.AddTool(
			mcp.NewTool(name,
				mcp.WithDescription(desc+" Saves site.yml and rebuilds."),
				mcp.WithArray("value", mcp.Required(), mcp.Description("New value for the section.")),
			),
			func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				raw := req.GetRawArguments()
				args, ok := raw.(map[string]any)
				if !ok {
					return mcp.NewToolResultError("expected a JSON object with a 'value' array"), nil
				}
				valueBytes, err := json.Marshal(args["value"])
				if err != nil {
					return mcp.NewToolResultError(err.Error()), nil
				}
				if err := d.updateSiteSection(section, strings.NewReader(string(valueBytes))); err != nil {
					return mcp.NewToolResultError(fmt.Sprintf("update %s failed (%d): %s", section, httpToStatus(err), err)), nil
				}
				return mcp.NewToolResultText(jsonMust(map[string]string{"section": section, "status": "updated"})), nil
			},
		)
	}
}

// ---------------------------------------------------------------------------
// Ops tools
// ---------------------------------------------------------------------------

func registerOpsTools(srv *mcpserver.MCPServer, d *Deps) {
	srv.AddTool(
		mcp.NewTool("regenerate",
			mcp.WithDescription("Force a full site rebuild (re-renders all pages, feeds, sitemap, and the search index)."),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			if err := d.Engine.Rebuild(); err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return mcp.NewToolResultText(jsonMust(map[string]string{"status": "ok"})), nil
		},
	)

	srv.AddTool(
		mcp.NewTool("get_stats",
			mcp.WithDescription("Return page-view stats: total, per-path, and per-day (30d)."),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			s, err := d.Store.Stats()
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return mcp.NewToolResultText(jsonMust(s)), nil
		},
	)

	srv.AddTool(
		mcp.NewTool("refresh_feeds",
			mcp.WithDescription("Poll all enabled RSS feeds and import new items."),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			n := d.Poller.Poll(ctx, d.Site())
			return mcp.NewToolResultText(jsonMust(map[string]int{"imported": n})), nil
		},
	)
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

// rawArgs returns the tool call's argument object, or nil if it wasn't one.
func rawArgs(req mcp.CallToolRequest) map[string]any {
	m, _ := req.GetRawArguments().(map[string]any)
	return m
}

// keepString returns the caller-supplied value for key, or fallback when the
// key was not supplied at all.
func keepString(args map[string]any, key, fallback string) string {
	v, ok := args[key]
	if !ok {
		return fallback
	}
	s, ok := v.(string)
	if !ok {
		return fallback
	}
	return s
}

// keepStrings is keepString for a JSON array of strings.
func keepStrings(args map[string]any, key string, fallback []string) []string {
	v, ok := args[key]
	if !ok {
		return fallback
	}
	arr, ok := v.([]any)
	if !ok {
		return fallback
	}
	out := make([]string, 0, len(arr))
	for _, item := range arr {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// jsonMust encodes v; a marshal failure is a programmer error and panics.
func jsonMust(v any) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		panic(fmt.Sprintf("mcp json marshal: %v", err))
	}
	return string(b)
}
