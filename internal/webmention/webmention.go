// Package webmention receives, verifies, and sends Webmentions.
package webmention

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"davidtorcivia.com/dtcom/internal/build"
	"davidtorcivia.com/dtcom/internal/markdown"
	"davidtorcivia.com/dtcom/internal/safehttp"
	"davidtorcivia.com/dtcom/internal/store"
	"golang.org/x/net/html"
)

const maxPage = 2 << 20

type Service struct {
	store    *store.Store
	baseURL  string
	postsDir string
	client   *http.Client
}

func New(st *store.Store, baseURL, postsDir string) *Service {
	return &Service{store: st, baseURL: strings.TrimRight(baseURL, "/"), postsDir: postsDir,
		client: &http.Client{Timeout: 20 * time.Second, Transport: safehttp.PublicTransport(10 * time.Second),
			CheckRedirect: func(_ *http.Request, via []*http.Request) error {
				if len(via) >= 3 {
					return fmt.Errorf("stopped after 3 redirects")
				}
				return nil
			}}}
}

func (s *Service) Receive(ctx context.Context, source, target string) (int64, error) {
	sourceURL, err := absoluteHTTP(source)
	if err != nil {
		return 0, fmt.Errorf("source: %w", err)
	}
	targetURL, err := absoluteHTTP(target)
	if err != nil {
		return 0, fmt.Errorf("target: %w", err)
	}
	base, _ := url.Parse(s.baseURL)
	if !strings.EqualFold(targetURL.Scheme, base.Scheme) || !strings.EqualFold(targetURL.Host, base.Host) ||
		!strings.HasPrefix(targetURL.Path, "/posts/") {
		return 0, fmt.Errorf("target is not a post on this site")
	}
	slug := strings.TrimPrefix(targetURL.Path, "/posts/")
	if slug == "" || strings.Contains(slug, "/") || !s.isPublished(slug) {
		return 0, fmt.Errorf("target is not a published post on this site")
	}
	id, err := s.store.QueueWebmention(sourceURL.String(), targetURL.String())
	if err != nil {
		return 0, err
	}
	go s.verify(context.WithoutCancel(ctx), id, sourceURL, targetURL)
	return id, nil
}

func (s *Service) isPublished(slug string) bool {
	articles, err := build.LoadArticles(s.postsDir)
	if err != nil {
		return false
	}
	for _, article := range articles {
		if article.Slug == slug {
			return !article.Draft && (article.PublishAt.IsZero() || !article.PublishAt.After(time.Now()))
		}
	}
	return false
}

func (s *Service) verify(ctx context.Context, id int64, source, target *url.URL) {
	page, _, err := s.fetch(ctx, source)
	status, title, message := "rejected", "", ""
	if err != nil {
		message = err.Error()
	} else {
		links, pageTitle := pageLinks(page, source)
		title = pageTitle
		for _, link := range links {
			if sameURL(link, target) {
				status = "verified"
				break
			}
		}
		if status != "verified" {
			message = "source does not link to target"
		}
	}
	if err := s.store.VerifyWebmention(id, status, title, message); err != nil {
		slog.Error("verify webmention", "id", id, "err", err)
	}
}

func (s *Service) Start(ctx context.Context) {
	s.SendAll(ctx)
	tick := time.NewTicker(time.Hour)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			s.SendAll(ctx)
		}
	}
}

func (s *Service) SendAll(ctx context.Context) {
	arts, err := build.LoadArticles(s.postsDir)
	if err != nil {
		slog.Warn("webmention scan", "err", err)
		return
	}
	now := time.Now()
	for _, article := range arts {
		if article.Draft || !article.PublishAt.IsZero() && article.PublishAt.After(now) {
			continue
		}
		source, _ := url.Parse(s.baseURL + "/posts/" + article.Slug)
		rendered, err := markdown.Render(article.Body)
		if err != nil {
			continue
		}
		links, _ := pageLinks([]byte(rendered), source)
		for _, target := range links {
			if target.Host == source.Host {
				continue
			}
			s.send(ctx, source, target)
		}
	}
}

func (s *Service) send(ctx context.Context, source, target *url.URL) {
	done, err := s.store.HasOutgoingWebmention(source.String(), target.String())
	if err != nil || done {
		return
	}
	page, header, err := s.fetch(ctx, target)
	if err != nil {
		_ = s.store.OutgoingWebmention(source.String(), target.String(), "failed", err.Error())
		return
	}
	endpoint := endpointFromLinkHeader(header.Get("Link"), target)
	if endpoint == nil {
		endpoint = endpointFromHTML(page, target)
	}
	if endpoint == nil {
		_ = s.store.OutgoingWebmention(source.String(), target.String(), "unsupported", "no webmention endpoint")
		return
	}
	form := url.Values{"source": {source.String()}, "target": {target.String()}}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "dtcom-webmention/1.0")
	resp, err := s.client.Do(req)
	if err == nil {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		resp.Body.Close()
	}
	status, message := "sent", ""
	if err != nil {
		status, message = "failed", err.Error()
	} else if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		status, message = "failed", resp.Status
	}
	_ = s.store.OutgoingWebmention(source.String(), target.String(), status, message)
}

func (s *Service) fetch(ctx context.Context, u *url.URL) ([]byte, http.Header, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	req.Header.Set("User-Agent", "dtcom-webmention/1.0")
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, resp.Header, fmt.Errorf("fetch %s: %s", u.Redacted(), resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxPage+1))
	if err != nil {
		return nil, resp.Header, err
	}
	if len(b) > maxPage {
		return nil, resp.Header, fmt.Errorf("page exceeds %d bytes", maxPage)
	}
	return b, resp.Header, nil
}

func absoluteHTTP(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("must be an absolute http(s) URL")
	}
	u.Fragment = ""
	return u, nil
}

func sameURL(a, b *url.URL) bool { return a.String() == b.String() }

func pageLinks(page []byte, base *url.URL) ([]*url.URL, string) {
	z := html.NewTokenizer(strings.NewReader(string(page)))
	var links []*url.URL
	title, inTitle := "", false
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			break
		}
		if tt == html.TextToken && inTitle {
			title += string(z.Text())
		}
		if tt != html.StartTagToken && tt != html.SelfClosingTagToken && tt != html.EndTagToken {
			continue
		}
		name, attrs := token(z)
		if name == "title" {
			inTitle = tt != html.EndTagToken
			continue
		}
		if name == "a" && tt != html.EndTagToken {
			if u := resolve(attrs["href"], base); u != nil {
				links = append(links, u)
			}
		}
	}
	return links, strings.TrimSpace(title)
}

func endpointFromHTML(page []byte, base *url.URL) *url.URL {
	z := html.NewTokenizer(strings.NewReader(string(page)))
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			return nil
		}
		if tt != html.StartTagToken && tt != html.SelfClosingTagToken {
			continue
		}
		name, attrs := token(z)
		if (name == "link" || name == "a") && relHas(attrs["rel"], "webmention") {
			return resolve(attrs["href"], base)
		}
	}
}

func endpointFromLinkHeader(value string, base *url.URL) *url.URL {
	for _, part := range strings.Split(value, ",") {
		lower := strings.ToLower(part)
		if !strings.Contains(lower, `rel="webmention"`) && !strings.Contains(lower, `rel='webmention'`) && !strings.Contains(lower, "rel=webmention") {
			continue
		}
		start, end := strings.IndexByte(part, '<'), strings.IndexByte(part, '>')
		if start >= 0 && end > start {
			return resolve(part[start+1:end], base)
		}
	}
	return nil
}

func token(z *html.Tokenizer) (string, map[string]string) {
	t := z.Token()
	attrs := map[string]string{}
	for _, a := range t.Attr {
		attrs[strings.ToLower(a.Key)] = a.Val
	}
	return strings.ToLower(t.Data), attrs
}

func relHas(rel, want string) bool {
	for _, field := range strings.Fields(strings.ToLower(rel)) {
		if strings.Trim(field, `"';`) == want {
			return true
		}
	}
	return false
}

func resolve(raw string, base *url.URL) *url.URL {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || raw == "" {
		return nil
	}
	u = base.ResolveReference(u)
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil
	}
	u.Fragment = ""
	return u
}
