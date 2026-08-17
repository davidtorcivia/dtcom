package build

import (
	"bytes"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"davidtorcivia.com/dtcom/internal/assets"
	"davidtorcivia.com/dtcom/internal/siteconfig"
)

type templateStore struct {
	tmpl *template.Template
}

// Load parses all *.html templates from dir, applying the given helper funcs.
func (t *templateStore) Load(templatesDir string, funcs template.FuncMap) error {
	tmpl, err := template.New("").Funcs(funcs).ParseGlob(filepath.Join(templatesDir, "*.html"))
	if err != nil {
		return err
	}
	t.tmpl = tmpl
	return nil
}

// render executes a named template to outPath.
//
// The output is buffered and written atomically: a template error partway
// through would otherwise leave a truncated page on disk, which the server
// would then happily serve.
func (t *templateStore) render(name, outPath string, data any) error {
	if t.tmpl == nil {
		return fmt.Errorf("templates not loaded")
	}
	var buf bytes.Buffer
	if err := t.tmpl.ExecuteTemplate(&buf, name, data); err != nil {
		return fmt.Errorf("execute %s: %w", name, err)
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return err
	}
	if old, err := os.ReadFile(outPath); err == nil && bytes.Equal(old, buf.Bytes()) {
		return nil
	}
	return writeAtomic(outPath, buf.Bytes())
}

// helperFuncs returns the template function map used across all templates.
func helperFuncs(fp *assets.Fingerprinter) template.FuncMap {
	return template.FuncMap{
		// asset appends a content hash to a /static/ URL so the file can be
		// cached hard and still update the moment it changes.
		"asset": fp.URL,
		// htmlSafe marks a string as pre-rendered HTML. Only ever applied to
		// values the author controls: the goldmark output for a post body and
		// the bio paragraphs from site.yml.
		"htmlSafe": func(s string) template.HTML { return template.HTML(s) },
		"formatDate": func(t time.Time) string {
			return t.Format("2006-01-02")
		},
		// formatDateLong renders a human-facing date ("31 January 2026") for
		// the byline, where the ISO form reads like a filename.
		"formatDateLong": func(t time.Time) string {
			return t.Format("2 January 2006")
		},
		"formatDateUnix": func(u int64) string {
			return time.Unix(u, 0).UTC().Format("2006-01-02")
		},
		// rfc3339 renders a machine-readable date for <time datetime>.
		"rfc3339":    func(t time.Time) string { return t.Format(time.RFC3339) },
		"socialIcon": socialIconSVG,
		// contactHref finds the site's contact address in the social list
		// instead of hardcoding one in the footer, where it could (and did)
		// drift out of step with site.yml.
		"contactHref": func(site *siteconfig.Config) string {
			if site == nil {
				return ""
			}
			for _, s := range site.Social {
				if s.Icon == "email" || strings.HasPrefix(strings.ToLower(s.Href), "mailto:") {
					return s.Href
				}
			}
			return ""
		},
		// absURL turns a site-relative path into an absolute one for og: tags
		// and canonical links, which require it.
		"absURL": func(site *siteconfig.Config, path string) string {
			return strings.TrimRight(site.BaseURL, "/") + path
		},
		// twitterHandle digs the site's X/Twitter handle out of the social
		// links so twitter:site and twitter:creator can be attributed without
		// a second place to configure it. Returns "" when there is no X entry,
		// and the template then omits the tags rather than emitting a bare @.
		"twitterHandle": func(site *siteconfig.Config) string {
			if site == nil {
				return ""
			}
			for _, s := range site.Social {
				if s.Icon != "x" {
					continue
				}
				// https://x.com/name or https://twitter.com/name → @name
				u := strings.TrimRight(s.Href, "/")
				if i := strings.LastIndex(u, "/"); i >= 0 {
					if handle := u[i+1:]; handle != "" {
						return "@" + strings.TrimPrefix(handle, "@")
					}
				}
			}
			return ""
		},
		// analyticsTag renders the configured tracker's <script> tag, or
		// nothing at all when none is configured.
		"analyticsTag": analyticsTag,
		// readingTime estimates minutes to read a post body, at the ~200 wpm
		// figure typical for prose.
		"readingTime": func(body string) int {
			words := len(strings.Fields(body))
			if words == 0 {
				return 1
			}
			return max(1, (words+199)/200)
		},
	}
}

// analyticsTag builds the analytics <script> tag from site.yml.
//
// Assembled in Go rather than in the template because the data-* attribute
// names are configuration: html/template will escape an attribute's value for
// you, but it cannot template an attribute *name* at all. Building the tag here
// means the name goes through ValidateAnalytics's character check and the value
// through the same escaper the templates use.
//
// A URL that is not http(s) yields nothing. Save-time validation already
// refuses one, but a site.yml edited by hand never passed through that, and the
// tag this returns is exempt from escaping by construction.
func analyticsTag(site *siteconfig.Config) template.HTML {
	if site == nil || !site.Analytics.Enabled() || site.Analytics.Origin() == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString(`<script defer src="`)
	template.HTMLEscape(&b, []byte(site.Analytics.ScriptURL))
	b.WriteString(`"`)
	for _, k := range site.Analytics.DataKeys() {
		if !siteconfig.ValidAttrName(k) {
			continue
		}
		b.WriteString(` data-` + k + `="`)
		template.HTMLEscape(&b, []byte(site.Analytics.Data[k]))
		b.WriteString(`"`)
	}
	b.WriteString(`></script>`)
	return template.HTML(b.String())
}

// socialIcons maps a site.yml social icon name to its inline SVG markup.
// The markup is lifted verbatim from the original index.html scaffolding so
// the rendered DOM matches what static/style.css expects (.social-icon).
var socialIcons = map[string]string{
	"x":         `<svg class="social-icon" viewBox="0 0 24 24" width="15" height="15" fill="currentColor"><path d="M18.244 2.25h3.308l-7.227 8.26 8.502 11.24H16.17l-5.214-6.817L4.99 21.75H1.68l7.73-8.835L1.254 2.25H8.08l4.713 6.231zm-1.161 17.52h1.833L7.084 4.126H5.117z"/></svg>`,
	"instagram": `<svg class="social-icon" viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="2.2"><rect x="2" y="2" width="20" height="20" rx="5" ry="5"/><path d="M16 11.37A4 4 0 1112.63 8 4 4 0 0116 11.37z"/><line x1="17.5" y1="6.5" x2="17.51" y2="6.5"/></svg>`,
	"github":    `<svg class="social-icon" viewBox="0 0 24 24" width="15" height="15" fill="currentColor"><path d="M12 0C5.37 0 0 5.37 0 12c0 5.31 3.435 9.795 8.205 11.385.6.105.825-.255.825-.57 0-.285-.015-1.23-.015-2.235-3.015.555-3.795-.735-4.035-1.41-.135-.345-.72-1.41-1.23-1.695-.42-.225-1.02-.78-.015-.795.945-.015 1.62.87 1.845 1.23 1.08 1.815 2.805 1.305 3.495.99.105-.78.42-1.305.765-1.605-2.67-.3-5.46-1.335-5.46-5.925 0-1.305.465-2.385 1.23-3.225-.12-.3-.54-1.53.12-3.18 0 0 1.005-.315 3.3 1.23.96-.27 1.98-.405 3-.405s2.04.135 3 .405c2.295-1.56 3.3-1.23 3.3-1.23.66 1.65.24 2.88.12 3.18.765.84 1.23 1.905 1.23 3.225 0 4.605-2.805 5.625-5.475 5.925.435.375.81 1.095.81 2.22 0 1.605-.015 2.895-.015 3.3 0 .315.225.69.825.57A12.02 12.02 0 0024 12c0-6.63-5.37-12-12-12z"/></svg>`,
	"substack":  `<svg class="social-icon" viewBox="0 0 24 24" width="15" height="15" fill="currentColor"><path d="M22.539 8.242H1.46V5.406h21.08v2.836zM1.46 10.812V24L12 18.11 22.54 24V10.812H1.46zM22.539 0H1.46v2.836h21.08V0z"/></svg>`,
	// Hugging Face's mark is the CC0 path from simple-icons. It is a face and
	// two hands rather than a letterform, so it goes muddy below about 14px.
	"huggingface": `<svg class="social-icon" viewBox="0 0 24 24" width="15" height="15" fill="currentColor"><path d="M12.025 1.13c-5.77 0-10.449 4.647-10.449 10.378 0 1.112.178 2.181.503 3.185.064-.222.203-.444.416-.577a.96.96 0 0 1 .524-.15c.293 0 .584.124.84.284.278.173.48.408.71.694.226.282.458.611.684.951v-.014c.017-.324.106-.622.264-.874s.403-.487.762-.543c.3-.047.596.06.787.203s.31.313.4.467c.15.257.212.468.233.542.01.026.653 1.552 1.657 2.54.616.605 1.01 1.223 1.082 1.912.055.537-.096 1.059-.38 1.572.637.121 1.294.187 1.967.187.657 0 1.298-.063 1.921-.178-.287-.517-.44-1.041-.384-1.581.07-.69.465-1.307 1.081-1.913 1.004-.987 1.647-2.513 1.657-2.539.021-.074.083-.285.233-.542.09-.154.208-.323.4-.467a1.08 1.08 0 0 1 .787-.203c.359.056.604.29.762.543s.247.55.265.874v.015c.225-.34.457-.67.683-.952.23-.286.432-.52.71-.694.257-.16.547-.284.84-.285a.97.97 0 0 1 .524.151c.228.143.373.388.43.625l.006.04a10.3 10.3 0 0 0 .534-3.273c0-5.731-4.678-10.378-10.449-10.378M8.327 6.583a1.5 1.5 0 0 1 .713.174 1.487 1.487 0 0 1 .617 2.013c-.183.343-.762-.214-1.102-.094-.38.134-.532.914-.917.71a1.487 1.487 0 0 1 .69-2.803m7.486 0a1.487 1.487 0 0 1 .689 2.803c-.385.204-.536-.576-.916-.71-.34-.12-.92.437-1.103.094a1.487 1.487 0 0 1 .617-2.013 1.5 1.5 0 0 1 .713-.174m-10.68 1.55a.96.96 0 1 1 0 1.921.96.96 0 0 1 0-1.92m13.838 0a.96.96 0 1 1 0 1.92.96.96 0 0 1 0-1.92M8.489 11.458c.588.01 1.965 1.157 3.572 1.164 1.607-.007 2.984-1.155 3.572-1.164.196-.003.305.12.305.454 0 .886-.424 2.328-1.563 3.202-.22-.756-1.396-1.366-1.63-1.32q-.011.001-.02.006l-.044.026-.01.008-.03.024q-.018.017-.035.036l-.032.04a1 1 0 0 0-.058.09l-.014.025q-.049.088-.11.19a1 1 0 0 1-.083.116 1.2 1.2 0 0 1-.173.18q-.035.029-.075.058a1.3 1.3 0 0 1-.251-.243 1 1 0 0 1-.076-.107c-.124-.193-.177-.363-.337-.444-.034-.016-.104-.008-.2.022q-.094.03-.216.087-.06.028-.125.063l-.13.074q-.067.04-.136.086a3 3 0 0 0-.135.096 3 3 0 0 0-.26.219 2 2 0 0 0-.12.121 2 2 0 0 0-.106.128l-.002.002a2 2 0 0 0-.09.132l-.001.001a1.2 1.2 0 0 0-.105.212q-.013.036-.024.073c-1.139-.875-1.563-2.317-1.563-3.203 0-.334.109-.457.305-.454m.836 10.354c.824-1.19.766-2.082-.365-3.194-1.13-1.112-1.789-2.738-1.789-2.738s-.246-.945-.806-.858-.97 1.499.202 2.362c1.173.864-.233 1.45-.685.64-.45-.812-1.683-2.896-2.322-3.295s-1.089-.175-.938.647 2.822 2.813 2.562 3.244-1.176-.506-1.176-.506-2.866-2.567-3.49-1.898.473 1.23 2.037 2.16c1.564.932 1.686 1.178 1.464 1.53s-3.675-2.511-4-1.297c-.323 1.214 3.524 1.567 3.287 2.405-.238.839-2.71-1.587-3.216-.642-.506.946 3.49 2.056 3.522 2.064 1.29.33 4.568 1.028 5.713-.624m5.349 0c-.824-1.19-.766-2.082.365-3.194 1.13-1.112 1.789-2.738 1.789-2.738s.246-.945.806-.858.97 1.499-.202 2.362c-1.173.864.233 1.45.685.64.451-.812 1.683-2.896 2.322-3.295s1.089-.175.938.647-2.822 2.813-2.562 3.244 1.176-.506 1.176-.506 2.866-2.567 3.49-1.898-.473 1.23-2.037 2.16c-1.564.932-1.686 1.178-1.464 1.53s3.675-2.511 4-1.297c.323 1.214-3.524 1.567-3.287 2.405.238.839 2.71-1.587 3.216-.642.506.946-3.49 2.056-3.522 2.064-1.29.33-4.568 1.028-5.713-.624"/></svg>`,
	"email":       `<svg class="social-icon" viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="2.2"><path d="M4 4h16c1.1 0 2 .9 2 2v12c0 1.1-.9 2-2 2H4c-1.1 0-2-.9-2-2V6c0-1.1.9-2 2-2z"/><polyline points="22,6 12,13 2,6"/></svg>`,
}

// socialIconSVG returns the inline SVG markup for a named social icon, or an
// empty HTML value if the name is unknown. Output is template.HTML so the SVG
// is injected raw (the markup is static and author-controlled, not user input).
func socialIconSVG(name string) template.HTML {
	if svg, ok := socialIcons[name]; ok {
		return template.HTML(svg)
	}
	return template.HTML("")
}

// SocialIconNames lists the icons a social link can use, sorted.
//
// The admin editor offers these as a dropdown rather than a free-text field:
// an unrecognised name renders as nothing at all, which looks like a bug
// rather than a typo. Sorted so the list does not reshuffle between builds —
// map iteration order would otherwise move the options around on every render.
func SocialIconNames() []string {
	out := make([]string, 0, len(socialIcons))
	for name := range socialIcons {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// HasSocialIcon reports whether name is a known icon.
func HasSocialIcon(name string) bool {
	_, ok := socialIcons[name]
	return ok
}
