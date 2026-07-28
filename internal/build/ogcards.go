package build

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"davidtorcivia.com/dtcom/internal/siteconfig"
)

// ogCardVersion is baked into every card's filename. Bump it when the design
// in ogimage.go changes, so existing cards get new URLs and are re-rendered
// instead of being served stale from the previous layout.
const ogCardVersion = 1

// ogCardURL renders a card if one does not already exist for this exact
// content, and returns its site-relative URL.
//
// The filename is a hash of what the card says, which does three things at
// once: identical content is rendered once, a changed title produces a new URL
// that scrapers will refetch rather than serving from their cache, and the
// rebuild that fires on every RSS poll skips the PNG encoding entirely because
// the file is already there.
func (e *Engine) ogCard(c OGCard, written *pathSet) (string, error) {
	payload := strings.Join([]string{
		c.Title, c.Subtitle, c.Kicker, c.Meta, fmt.Sprint(ogCardVersion),
	}, "\x00")
	sum := sha256.Sum256([]byte(payload))
	name := hex.EncodeToString(sum[:])[:16] + ".png"
	path := filepath.Join(e.cfg.PublicDir, "og", name)

	if _, err := os.Stat(path); err != nil {
		data, renderErr := RenderOGCard(c)
		if renderErr != nil {
			return "", renderErr
		}
		if err := e.writeFile(path, data, written); err != nil {
			return "", err
		}
	} else {
		// Already on disk from an earlier rebuild — still has to be recorded,
		// or prune() would delete it as stale output.
		written.add(path)
	}
	return "/og/" + name, nil
}

// articleOGImage is the absolute URL of a post's social preview.
//
// An explicit `cover` in the frontmatter wins: the author chose that image on
// purpose, and a generated card is the fallback for the common case of a post
// that has none. Before this existed those posts advertised no image at all
// and unfurled as a bare link.
func (e *Engine) articleOGImage(a Article, site *siteconfig.Config, written *pathSet) (string, error) {
	base := strings.TrimRight(baseURL(site), "/")
	if a.Cover != "" {
		if strings.HasPrefix(a.Cover, "http://") || strings.HasPrefix(a.Cover, "https://") {
			return a.Cover, nil
		}
		return base + "/" + strings.TrimLeft(a.Cover, "/"), nil
	}
	rel, err := e.ogCard(OGCard{
		Title:    a.Title,
		Subtitle: a.Description,
		Kicker:   site.Title,
		Meta:     a.Date.Format("2006-01-02"),
	}, written)
	if err != nil {
		return "", err
	}
	return base + rel, nil
}

// siteOGImage is the card used by the pages that are not a single post: the
// home page, /links, /search and the 404. They share one card because they
// describe the site rather than a piece of writing.
func (e *Engine) siteOGImage(site *siteconfig.Config, written *pathSet) (string, error) {
	host := strings.TrimPrefix(strings.TrimPrefix(baseURL(site), "https://"), "http://")
	rel, err := e.ogCard(OGCard{
		Title:    site.Title,
		Subtitle: site.Description,
		Kicker:   strings.TrimRight(host, "/"),
	}, written)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(baseURL(site), "/") + rel, nil
}
