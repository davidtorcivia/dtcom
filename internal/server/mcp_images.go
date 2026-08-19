package server

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"davidtorcivia.com/dtcom/internal/build"
)

// Image tools.
//
// A model writing a post could edit the markdown but had no way to see what
// pictures existed or to add one, so every image had to be put in by hand
// through the admin UI first. list_images answers the first half and add_image
// the second.
//
// The conventions below are the other half of the problem. Nothing in the
// markdown says "this is a figure" or "this pair swaps with the theme" — the
// renderer infers both from layout, and a caller that does not know the rules
// writes something that looks right and renders as two loose images. So the
// rules travel with the tools that need them.

// figureConventions is appended to the description of every tool that takes or
// returns markdown body text. It is the only place a caller learns what the
// renderer does with an image, and getting it wrong is silent: the post builds,
// it just does not come out as a figure.
const figureConventions = "\n\nFigures: an image alone in its paragraph renders as a <figure> " +
	"captioned by its alt text — so write the caption in the brackets, not as a line underneath. " +
	"An image used mid-sentence stays inline and gets no caption.\n\n" +
	"Light/dark: two images on CONSECUTIVE lines with no blank line between them, whose URLs end " +
	"in #light and #dark, collapse into ONE figure that swaps with the reader's theme. The caption " +
	"goes on the first of the two; the second's alt is left empty. A blank line between them breaks " +
	"the pair and yields two separate images instead. For example:\n" +
	"![The transfer function](/images/a.png#light)\n" +
	"![](/images/b.png#dark)"

type listImagesArgs struct {
	Query string `json:"query,omitempty" jsonschema:"Optional substring to filter filenames by."`
}

// storedImage is one uploaded picture as list_images reports it.
type storedImage struct {
	URL string `json:"url"`
	// Theme is "light" or "dark" when this file is one half of a themed pair
	// that is already on disk, and empty otherwise. It saves the caller
	// guessing from filenames which of two pictures is which.
	Theme string `json:"theme,omitempty"`
	// Pair is the URL of the other half, when there is one.
	Pair  string `json:"pair,omitempty"`
	Bytes int64  `json:"bytes"`
}

// Wrapped in an object for the reason given beside articleListResult in
// mcp.go: a bare array is not a legal structuredContent shape.
type imageListResult struct {
	Images []storedImage `json:"images" jsonschema:"Images already uploaded, by URL."`
}

type addImageArgs struct {
	URL string `json:"url,omitempty" jsonschema:"Public http(s) URL to download the image from. Preferred: it costs a few tokens where data costs millions. Private and loopback addresses are refused."`
	// Offered because some clients hold bytes with nowhere to put them, and
	// warned about because a model that writes the field itself will spend its
	// whole context doing it — a 2 MB photo is ~2.7 million characters here.
	Data     string `json:"data,omitempty" jsonschema:"Base64-encoded image bytes, as an alternative to url. Only practical for very small images: the encoded value is ~1.4x the file size and is part of this tool call."`
	Filename string `json:"filename,omitempty" jsonschema:"Original filename, for the log only. The stored name is a hash of the content."`
	Theme    string `json:"theme,omitempty" jsonschema:"\"light\" or \"dark\" to tag the returned markdown as one half of a theme-swapping figure. Upload both halves and put the two returned lines on consecutive lines with no blank line between them."`
	Alt      string `json:"alt,omitempty" jsonschema:"Caption text. An image alone in its paragraph renders as a figure captioned by this."`
}

type addImageResult struct {
	URL string `json:"url"`
	// Markdown is the line to paste into a body, already carrying the caption
	// and the theme tag. Returning it means the conventions are followed by
	// construction rather than by the caller remembering them.
	Markdown string `json:"markdown"`
	Bytes    int    `json:"bytes"`
}

func registerImageTools(srv *mcp.Server, d *Deps) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_images",
		Annotations: reads("List uploaded images"),
		Description: "List the images already uploaded to the site, so a post can reference one " +
			"without uploading it again. Renditions and generated variants are not listed — only " +
			"the originals, which are the URLs a post should use." + figureConventions,
	}, func(ctx context.Context, req *mcp.CallToolRequest, args listImagesArgs) (*mcp.CallToolResult, imageListResult, error) {
		imgs, err := d.listStoredImages(args.Query)
		if err != nil {
			return nil, imageListResult{}, err
		}
		return nil, imageListResult{Images: imgs}, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "add_image",
		Annotations: writes("Add an image", false),
		Description: "Store an image on the site and return the markdown line for it. Give " +
			"either url (preferred) or data. The file is named by a hash of its contents, so " +
			"uploading the same picture twice reuses one file, and responsive renditions are " +
			"generated in the background." + figureConventions,
	}, func(ctx context.Context, req *mcp.CallToolRequest, args addImageArgs) (*mcp.CallToolResult, addImageResult, error) {
		raw, err := imageBytesFrom(ctx, args)
		if err != nil {
			return nil, addImageResult{}, err
		}
		url, err := d.storeImageBytes(raw)
		if err != nil {
			return nil, addImageResult{}, err
		}
		return nil, addImageResult{
			URL:      url,
			Markdown: imageMarkdown(url, args.Alt, args.Theme),
			Bytes:    len(raw),
		}, nil
	})
}

// imageBytesFrom resolves whichever source the caller supplied. Exactly one:
// asking for both is a mistake worth surfacing, since silently preferring one
// would leave the caller believing the other had been used.
func imageBytesFrom(ctx context.Context, args addImageArgs) ([]byte, error) {
	hasURL := strings.TrimSpace(args.URL) != ""
	hasData := strings.TrimSpace(args.Data) != ""
	switch {
	case hasURL && hasData:
		return nil, fmt.Errorf("give either url or data, not both")
	case hasURL:
		return fetchRemoteImage(ctx, strings.TrimSpace(args.URL))
	case hasData:
		// Both alphabets, with or without padding: a caller pasting a data:
		// URI's payload or a URL-safe encoding should not have to know which
		// decoder this end happens to use.
		s := strings.TrimSpace(args.Data)
		if _, body, ok := strings.Cut(s, ","); ok && strings.HasPrefix(s, "data:") {
			s = body
		}
		for _, enc := range []*base64.Encoding{
			base64.StdEncoding, base64.RawStdEncoding,
			base64.URLEncoding, base64.RawURLEncoding,
		} {
			if b, err := enc.DecodeString(s); err == nil {
				return b, nil
			}
		}
		return nil, fmt.Errorf("data is not valid base64")
	default:
		return nil, fmt.Errorf("give either url or data")
	}
}

// imageMarkdown assembles the line to paste into a body: the caption in the alt
// slot where the renderer looks for it, and the theme tag on the URL.
func imageMarkdown(url, alt, theme string) string {
	switch strings.ToLower(strings.TrimSpace(theme)) {
	case "light":
		url += "#light"
	case "dark":
		url += "#dark"
	}
	return "![" + strings.TrimSpace(alt) + "](" + url + ")"
}

// listStoredImages reads the images directory, reporting only the originals.
//
// Renditions are excluded because they are not a choice a post gets to make:
// the renderer picks a width per reader from the srcset it builds, and a post
// that pointed at one directly would pin every reader to that size.
func (d *Deps) listStoredImages(query string) ([]storedImage, error) {
	entries, err := os.ReadDir(d.Cfg.ImagesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []storedImage{}, nil
		}
		return nil, err
	}
	query = strings.ToLower(strings.TrimSpace(query))

	out := make([]storedImage, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !build.IsMasterImage(e.Name()) {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(e.Name()), query) {
			continue
		}
		var size int64
		if info, err := e.Info(); err == nil {
			size = info.Size()
		}
		out = append(out, storedImage{URL: "/images/" + e.Name(), Bytes: size})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].URL < out[j].URL })
	markThemePairs(out, d.postsDir())
	return out, nil
}

// markThemePairs fills in Theme and Pair for images already used as a
// light/dark pair somewhere in the posts.
//
// Read off the posts rather than guessed from filenames, because the stored
// name is a content hash — it says nothing about what the picture is for. The
// post that uses them is the only place that record exists, and it is also the
// answer the caller actually wants: "which of these two is the dark one" is
// settled by how they were used, not by what they were called on someone's
// desktop.
func markThemePairs(imgs []storedImage, postsDir string) {
	arts, err := build.LoadArticles(postsDir)
	if err != nil {
		return // best-effort: the listing is still useful without pairings
	}
	theme := map[string]string{} // image URL -> "light" | "dark"
	partner := map[string]string{}
	for _, a := range arts {
		for _, pair := range themePairsIn(a.Body) {
			theme[pair[0]] = "light"
			theme[pair[1]] = "dark"
			partner[pair[0]] = pair[1]
			partner[pair[1]] = pair[0]
		}
	}
	for i := range imgs {
		imgs[i].Theme = theme[imgs[i].URL]
		imgs[i].Pair = partner[imgs[i].URL]
	}
}

// themePairsIn finds the (light, dark) image URLs of every themed figure in a
// markdown body, matching the renderer's own rule: the two lines must be
// adjacent, with no blank line between them.
func themePairsIn(body string) [][2]string {
	lines := strings.Split(body, "\n")
	var out [][2]string
	for i := 0; i+1 < len(lines); i++ {
		a, aTheme, ok := taggedImageURL(lines[i])
		if !ok {
			continue
		}
		b, bTheme, ok := taggedImageURL(lines[i+1])
		if !ok {
			continue
		}
		switch {
		case aTheme == "light" && bTheme == "dark":
			out = append(out, [2]string{a, b})
		case aTheme == "dark" && bTheme == "light":
			out = append(out, [2]string{b, a})
		}
	}
	return out
}

// taggedImageURL pulls the URL and theme out of a line that is nothing but one
// theme-tagged image, which is the only shape the renderer pairs.
func taggedImageURL(line string) (url, theme string, ok bool) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "![") || !strings.HasSuffix(line, ")") {
		return "", "", false
	}
	open := strings.Index(line, "](")
	if open < 0 {
		return "", "", false
	}
	url = line[open+2 : len(line)-1]
	for _, t := range []string{"light", "dark"} {
		if trimmed, found := strings.CutSuffix(url, "#"+t); found {
			return trimmed, t, true
		}
	}
	return "", "", false
}
