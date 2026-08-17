package server

import (
	"cmp"
	"log/slog"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"davidtorcivia.com/dtcom/internal/build"
	"davidtorcivia.com/dtcom/internal/markdown"
	"davidtorcivia.com/dtcom/internal/siteconfig"
	"davidtorcivia.com/dtcom/internal/store"
)

// maxAdminForm caps an admin form POST. Article bodies are the largest thing
// posted here; 10 MB is far beyond any hand-written essay and keeps
// ParseForm from buffering an unbounded body.
const maxAdminForm = 10 << 20

// registerAdmin wires the session-cookie-authenticated admin UI. Every route
// other than the login form/login POST is wrapped in requireAuth.
func registerAdmin(mux *http.ServeMux, d *Deps) {
	mux.HandleFunc("GET /admin/login", d.adminLoginForm)
	mux.HandleFunc("POST /admin/login", d.adminLogin)
	mux.HandleFunc("POST /admin/logout", d.requireAuth(d.adminLogout))
	mux.HandleFunc("GET /admin", d.requireAuth(d.adminDashboard))
	mux.HandleFunc("GET /admin/{$}", d.requireAuth(d.adminDashboard))
	mux.HandleFunc("GET /admin/posts", d.requireAuth(d.adminPostsList))
	mux.HandleFunc("GET /admin/posts/new", d.requireAuth(d.adminPostEdit))
	mux.HandleFunc("GET /admin/posts/{slug}/edit", d.requireAuth(d.adminPostEdit))
	mux.HandleFunc("POST /admin/posts/save", d.requireAuth(d.adminPostSave))
	mux.HandleFunc("POST /admin/posts/preview", d.requireAuth(d.adminPostPreview))
	mux.HandleFunc("POST /admin/posts/{slug}/delete", d.requireAuth(d.adminPostDelete))
	mux.HandleFunc("GET /admin/links", d.requireAuth(d.adminLinksList))
	mux.HandleFunc("POST /admin/links/add", d.requireAuth(d.adminLinkAdd))
	mux.HandleFunc("POST /admin/links/style", d.requireAuth(d.adminLinksStyle))
	mux.HandleFunc("POST /admin/links/{id}/delete", d.requireAuth(d.adminLinkDelete))
	mux.HandleFunc("GET /admin/site", d.requireAuth(d.adminSiteEdit))
	mux.HandleFunc("POST /admin/site", d.requireAuth(d.adminSiteSave))
	mux.HandleFunc("POST /admin/site/favicon", d.requireAuth(d.adminFaviconUpload))
	mux.HandleFunc("POST /admin/site/favicon/reset", d.requireAuth(d.adminFaviconReset))
	mux.HandleFunc("POST /admin/images", d.requireAuth(d.adminImageUpload))
	mux.HandleFunc("GET /admin/integrations", d.requireAuth(d.adminIntegrations))
	mux.HandleFunc("POST /admin/tokens", d.requireAuth(d.adminTokenCreate))
	mux.HandleFunc("POST /admin/tokens/{id}/revoke", d.requireAuth(d.adminTokenRevoke))
	mux.HandleFunc("POST /admin/regenerate", d.requireAuth(d.adminRegenerate))
	registerAdminBackups(mux, d)
	registerAdminFeeds(mux, d)
	registerAdminSiteLists(mux, d)
}

// requireAuth redirects unauthenticated requests to the login page and rejects
// cross-site state-changing requests. Authenticated same-origin requests fall
// through to the wrapped handler.
func (d *Deps) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := d.Auth.SessionUser(r); !ok {
			// no-store so a browser never serves a cached admin page to the
			// next person at the machine.
			w.Header().Set("Cache-Control", "no-store")
			http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && !sameOrigin(r, d.baseURL()) {
			writeError(w, http.StatusForbidden, nil)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		next(w, r)
	}
}

func (d *Deps) baseURL() string {
	if d.Cfg == nil {
		return ""
	}
	return d.Cfg.BaseURL
}

// adminReady reports whether the admin templates loaded. Without them the
// admin UI cannot render at all, which is a server-configuration problem
// (503), not a client error.
func (d *Deps) adminReady(w http.ResponseWriter) bool {
	if d.adminTmpls == nil {
		writeError(w, http.StatusServiceUnavailable, nil)
		return false
	}
	return true
}

// adminData is the base map every admin page renders with. Every page must
// carry a Title (consumed by the admin-header partial) plus its own fields.
//
// Favicon comes along so the admin chrome shows the same icon as the public
// site. It is read defensively: the login page renders before the site config
// is necessarily usable, and a nil config there should not panic the one page
// an operator needs in order to fix things.
func (d *Deps) adminData(title string, extra map[string]any) map[string]any {
	out := map[string]any{"Title": title}
	if site := d.Site(); site != nil {
		out["Favicon"] = site.Favicon
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

// parseAdminForm bounds and parses an admin form POST, handling both encodings
// the admin UI actually sends.
//
// This has to branch on the content type. r.ParseForm reads the body only for
// application/x-www-form-urlencoded; for a multipart body it succeeds while
// leaving the values unparsed, and — because it sets r.Form — it also stops
// the later r.FormValue from parsing them itself. The result was that the
// editor's Preview, which posts a FormData, silently rendered an empty body.
func parseAdminForm(w http.ResponseWriter, r *http.Request) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxAdminForm)
	var err error
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		err = r.ParseMultipartForm(4 << 20)
	} else {
		err = r.ParseForm()
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return false
	}
	return true
}

func (d *Deps) adminLoginForm(w http.ResponseWriter, r *http.Request) {
	if !d.adminReady(w) {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	d.adminTmpls.render(w, "login", d.adminData("Login", nil))
}

func (d *Deps) adminLogin(w http.ResponseWriter, r *http.Request) {
	if !d.adminReady(w) {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if !sameOrigin(r, d.baseURL()) {
		writeError(w, http.StatusForbidden, nil)
		return
	}
	// Throttle before doing any bcrypt work: the hash comparison is the
	// expensive half of this handler and the reason an unthrottled login is a
	// CPU-exhaustion lever as well as a brute-force one.
	if !d.limits.login.Allow(d.clientIP(r)) || !d.limits.loginGlobal.Allow("global") {
		slog.Warn("login rate limited", "ip", d.clientIP(r))
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusTooManyRequests)
		d.adminTmpls.render(w, "login", d.adminData("Login", map[string]any{
			"Error": "Too many attempts. Wait a moment and try again.",
		}))
		return
	}
	if !parseAdminForm(w, r) {
		return
	}
	if !d.Auth.CheckPasswordAndTOTP(r.FormValue("password"), r.FormValue("totp")) {
		slog.Warn("failed admin login", "ip", d.clientIP(r))
		w.WriteHeader(http.StatusUnauthorized)
		d.adminTmpls.render(w, "login", d.adminData("Login", map[string]any{"Error": "Invalid credentials"}))
		return
	}
	if err := d.Auth.SetSession(w, "admin"); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	slog.Info("admin login", "ip", d.clientIP(r))
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (d *Deps) adminLogout(w http.ResponseWriter, r *http.Request) {
	d.Auth.ClearSession(w)
	http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
}

// barRow is a stats row scaled to the largest value in its set, so the
// template can draw a proportional bar without doing arithmetic itself.
type barRow struct {
	Path string
	// Label is the row's text when it names something that is not a page — a
	// referring site, a place — and so must not be rendered as a link.
	Label   string
	Day     string
	Count   int64
	Percent int
	// Share is the row's slice of the window's total views, which is the
	// question a "most read" list is really being asked.
	Share int
}

// chartBar is one column of the views chart.
type chartBar struct {
	Key     string // the day or month it covers, machine form
	Label   string // "12 Jul 2026" / "Jul 2026"
	Count   int64
	Percent int
	// Tick marks a column worth labelling on the axis — the first of a month
	// on a daily chart, January on a monthly one. Without it a 90-day chart
	// has ninety labels or two.
	Tick     bool
	TickText string
}

// maxDashboardRows bounds the "most read" list; the full breakdown is
// available through /api/v1/stats.
const maxDashboardRows = 10

// dashboardRange is one option in the range selectors above the charts.
//
// Days counts back from today; zero means everything there is. The set is
// deliberately short — these are the spans a person actually asks for, and a
// free-text date picker on a personal dashboard is a form to fill in rather
// than a number to read.
type dashboardRange struct {
	Key   string
	Label string
	Days  int
}

var dashboardRanges = []dashboardRange{
	{"7d", "7 days", 7},
	{"30d", "30 days", 30},
	{"90d", "90 days", 90},
	{"12m", "12 months", 365},
	{"all", "All time", 0},
}

const (
	defaultChartRange = "30d"
	defaultTopRange   = "30d"
)

// lookupRange finds a range by key, falling back to def for anything absent or
// unrecognised — this comes from the query string.
func lookupRange(key, def string) dashboardRange {
	for _, r := range dashboardRanges {
		if r.Key == key {
			return r
		}
	}
	for _, r := range dashboardRanges {
		if r.Key == def {
			return r
		}
	}
	return dashboardRanges[0]
}

// since returns the first day the range covers, given the day history starts.
// The empty string means "no lower bound", which only the all-time range wants
// for its counting queries.
func (r dashboardRange) since(today time.Time, first string) string {
	if r.Days == 0 {
		return first
	}
	return today.AddDate(0, 0, -(r.Days - 1)).Format("2006-01-02")
}

// rangeOption is a range as the template draws it: one <option> in a range
// selector. It used to be a link per range, which on a phone was five tabs in
// a row that ran off the side of the panel.
type rangeOption struct {
	Key    string
	Label  string
	Active bool
}

func rangeOptions(selected string) []rangeOption {
	out := make([]rangeOption, 0, len(dashboardRanges))
	for _, r := range dashboardRanges {
		out = append(out, rangeOption{Key: r.Key, Label: r.Label, Active: r.Key == selected})
	}
	return out
}

func (d *Deps) adminDashboard(w http.ResponseWriter, r *http.Request) {
	if !d.adminReady(w) {
		return
	}
	arts, err := build.LoadArticles(d.postsDir())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	links, err := d.Store.ListLinks(500)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	var published, drafts int
	for _, a := range arts {
		if a.Draft {
			drafts++
		} else {
			published++
		}
	}
	recent := arts
	if len(recent) > 5 {
		recent = recent[:5]
	}

	today := time.Now().UTC()
	todayStr := today.Format("2006-01-02")
	firstDay, err := d.Store.FirstViewDay()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if firstDay == "" {
		firstDay = todayStr // nothing recorded yet; an empty window of one day
	}

	chartRange := lookupRange(r.URL.Query().Get("chart"), defaultChartRange)
	topRange := lookupRange(r.URL.Query().Get("top"), defaultTopRange)

	chart, chartErr := d.viewsChart(chartRange, today, firstDay)
	if chartErr != nil {
		writeError(w, http.StatusInternalServerError, chartErr)
		return
	}

	// Most read, scaled against the busiest path and against the window's total
	// so each row can show both its bar and its share.
	topSince := topRange.since(today, firstDay)
	if topRange.Days == 0 {
		topSince = "" // all time: no lower bound at all
	}
	topPaths, err := d.Store.TopPaths(topSince, maxDashboardRows)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	topTotal, err := d.Store.TotalViews(topSince)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	var maxPath int64
	for _, p := range topPaths {
		maxPath = max(maxPath, p.Count)
	}
	titles := pageTitles(arts)
	topRows := make([]barRow, 0, len(topPaths))
	for _, p := range topPaths {
		topRows = append(topRows, barRow{
			Path:    p.Path,
			Label:   cmp.Or(titles[p.Path], p.Path),
			Count:   p.Count,
			Percent: percentOf(p.Count, maxPath),
			Share:   percentOf(p.Count, topTotal),
		})
	}

	total, err := d.Store.TotalViews("")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	todayCount, err := d.Store.TotalViews(todayStr)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	// The audience panels share the "most read" range, since they answer
	// questions about the same window: who came, from where, and for how long.
	visitors, err := d.Store.UniqueVisitors(topSince)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	referrers, err := d.Store.TopReferrers(topSince, maxDashboardRows)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	places, err := d.Store.TopPlaces(topSince, maxDashboardRows)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	dwell, err := d.Store.MedianDwell(topSince)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	allVisitors, err := d.Store.UniqueVisitors("")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	d.adminTmpls.render(w, "dashboard", d.adminData("Dashboard", map[string]any{
		"Site":       d.Site(),
		"PostCount":  published,
		"DraftCount": drafts,
		"LinkCount":  len(links),
		"Total":      total,
		"Today":      todayCount,
		"Recent":     recent,

		"Chart":        chart.bars,
		"ChartTotal":   chart.total,
		"ChartPeak":    chart.peak,
		"ChartPeakAt":  chart.peakAt,
		"ChartMax":     chart.scale,
		"ChartGrain":   chart.grain,
		"ChartStart":   chart.start,
		"ChartEnd":     chart.end,
		"ChartRange":   chartRange,
		"ChartRanges":  rangeOptions(chartRange.Key),
		"TopPaths":     topRows,
		"TopRange":     topRange,
		"TopRanges":    rangeOptions(topRange.Key),
		"TopRangeSum":  topTotal,
		"Visitors":     visitors,
		"AllVisitors":  allVisitors,
		"PagesEach":    pagesEach(topTotal, visitors),
		"Dwell":        humanDuration(dwell),
		"Referrers":    labelRows(referrers, topTotal),
		"Places":       labelRows(places, topTotal),
		"AnalyticsOn":  d.Site() != nil && d.Site().Analytics.Enabled(),
		"AnalyticsURL": analyticsDashboardURL(d.Site()),
	}))
}

// viewsChart is the assembled bar chart for one range.
type viewsChart struct {
	bars   []chartBar
	total  int64
	peak   int64
	peakAt string
	scale  int64  // the value the tallest bar represents, i.e. the y-axis top
	grain  string // "day" or "month", for the panel's subtitle
	start  string
	end    string
}

// chartDayLimit is how many days a chart will draw one bar each for. Past it
// the window is bucketed by month: ninety bars across a panel is already a bar
// every few pixels, and a year of them is a smear.
const chartDayLimit = 92

func (d *Deps) viewsChart(rng dashboardRange, today time.Time, firstDay string) (*viewsChart, error) {
	since := rng.since(today, firstDay)
	until := today.Format("2006-01-02")

	// A day per bar while the window is short enough to read; months beyond.
	byDay := true
	if rng.Days == 0 || rng.Days > chartDayLimit {
		start, err := time.Parse("2006-01-02", since)
		if err != nil {
			return nil, err
		}
		byDay = today.Sub(start) <= chartDayLimit*24*time.Hour
	}

	var (
		buckets []store.Bucket
		err     error
	)
	c := &viewsChart{grain: "month"}
	if byDay {
		c.grain = "day"
		buckets, err = d.Store.ViewsByDay(since, until)
	} else {
		buckets, err = d.Store.ViewsByMonth(since, until)
	}
	if err != nil {
		return nil, err
	}

	for _, b := range buckets {
		c.total += b.Count
		if b.Count > c.peak {
			c.peak, c.peakAt = b.Count, b.Label
		}
	}
	// Scale to a round number at or above the peak, never to zero: an empty
	// window should draw an empty chart of the right height rather than
	// collapse to nothing. The true peak is in the summary line above, so the
	// axis is free to be readable instead of exact.
	c.scale = niceCeil(c.peak)

	c.bars = make([]chartBar, 0, len(buckets))
	for _, b := range buckets {
		bar := chartBar{
			Key:     b.Key,
			Label:   b.Label,
			Count:   b.Count,
			Percent: percentOf(b.Count, c.scale),
		}
		bar.Tick, bar.TickText = axisTick(b.Key, byDay)
		c.bars = append(c.bars, bar)
	}
	if len(buckets) > 0 {
		c.start, c.end = buckets[0].Label, buckets[len(buckets)-1].Label
	}
	return c, nil
}

// niceCeil rounds a chart's peak up to a value worth printing on an axis: two
// significant figures, and even.
//
// Both halves earn their keep. Two significant figures means a peak of 291
// draws an axis topped at 300 rather than 291, and wastes at most a few per
// cent of the plot doing it. Even means the midpoint gridline can be labelled
// with a whole number — labelling the geometric middle of a 7-view chart as "4"
// put the number half a view away from the line it belonged to.
func niceCeil(n int64) int64 {
	if n <= 2 {
		return 2
	}
	mag := int64(1)
	for n/mag >= 100 {
		mag *= 10
	}
	v := ((n + mag - 1) / mag) * mag
	if v%2 != 0 {
		v += mag
	}
	return v
}

// axisTick decides whether a column earns a label on the axis: the first of the
// month on a daily chart, January on a monthly one.
func axisTick(key string, byDay bool) (bool, string) {
	if byDay {
		day, err := time.Parse("2006-01-02", key)
		if err != nil || day.Day() != 1 {
			return false, ""
		}
		return true, day.Format("Jan")
	}
	month, err := time.Parse("2006-01", key)
	if err != nil || month.Month() != time.January {
		return false, ""
	}
	return true, month.Format("2006")
}

// analyticsDashboardURL points at the configured tracker's own site, so the
// panel can offer a way through to the numbers this dashboard does not keep.
// The script URL's origin is the best guess available without asking for a
// second setting nobody wants to fill in.
func analyticsDashboardURL(site *siteconfig.Config) string {
	if site == nil {
		return ""
	}
	return site.Analytics.Origin()
}

// percentOf scales n against maxN for a bar width, with a floor of 2% so a
// non-zero value is always visible.
func percentOf(n, maxN int64) int {
	if maxN <= 0 || n <= 0 {
		return 0
	}
	p := int(n * 100 / maxN)
	return max(2, p)
}

// pageTitles maps every path the beacon can record to what the page is called,
// so "most read" reads like a list of writing rather than a list of URLs. A
// path with no entry (a post deleted since it was read) falls back to itself.
func pageTitles(arts []build.Article) map[string]string {
	titles := map[string]string{"/": "Home", "/links": "Links", "/search": "Search"}
	for _, a := range arts {
		titles["/posts/"+a.Slug] = a.Title
	}
	return titles
}

// labelRows scales a set of counted labels against the busiest one, the same
// way topRows does for paths.
func labelRows(counts []store.LabelCount, total int64) []barRow {
	var top int64
	for _, c := range counts {
		top = max(top, c.Count)
	}
	out := make([]barRow, 0, len(counts))
	for _, c := range counts {
		out = append(out, barRow{
			Label:   c.Label,
			Count:   c.Count,
			Percent: percentOf(c.Count, top),
			Share:   percentOf(c.Count, total),
		})
	}
	return out
}

// pagesEach is views per visitor, to one decimal — how far into the site a
// typical arrival got. It is not a session length: two visits a week apart from
// the same address count as one visitor reading more, since nothing here
// carries a session.
func pagesEach(views, visitors int64) string {
	if visitors <= 0 {
		return "—"
	}
	return strconv.FormatFloat(float64(views)/float64(visitors), 'f', 1, 64)
}

// humanDuration renders seconds the way a person says them.
func humanDuration(secs int64) string {
	switch {
	case secs <= 0:
		return "—"
	case secs < 60:
		return strconv.FormatInt(secs, 10) + "s"
	default:
		return strconv.FormatInt(secs/60, 10) + "m " + strconv.FormatInt(secs%60, 10) + "s"
	}
}

func (d *Deps) adminPostsList(w http.ResponseWriter, r *http.Request) {
	if !d.adminReady(w) {
		return
	}
	arts, err := build.LoadArticles(d.postsDir())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	d.adminTmpls.render(w, "posts-list", d.adminData("Posts", map[string]any{"Articles": arts}))
}

func (d *Deps) adminPostEdit(w http.ResponseWriter, r *http.Request) {
	if !d.adminReady(w) {
		return
	}
	slug := r.PathValue("slug")
	data := d.adminData("New Post", nil)
	if slug != "" {
		a, err := d.findArticleBySlug(slug)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		if a == nil {
			writeError(w, http.StatusNotFound, nil)
			return
		}
		data["Title"] = "Edit Post"
		data["Article"] = *a
	}
	d.adminTmpls.render(w, "post-edit", data)
}

// adminPostSave handles both create and update. The form posts the existing
// slug (hidden field) when editing; an empty value means a new post, in which
// case we derive the slug from the title and the file is created.
func (d *Deps) adminPostSave(w http.ResponseWriter, r *http.Request) {
	if !d.adminReady(w) {
		return
	}
	if !parseAdminForm(w, r) {
		return
	}
	origSlug := r.FormValue("slug")
	date := r.FormValue("date")
	if date == "" {
		date = time.Now().Format("2006-01-02")
	}
	in := articleInput{
		Title:       strings.TrimSpace(r.FormValue("title")),
		Date:        date,
		Description: strings.TrimSpace(r.FormValue("description")),
		Tags:        parseTags(r.FormValue("tags")),
		Body:        r.FormValue("body"),
		Draft:       r.FormValue("draft") == "on",
	}

	// Editing an existing post: reuse its slug and overwrite its source file.
	if origSlug != "" {
		if _, err := d.updateArticle(origSlug, in); err != nil {
			d.renderPostEditError(w, in, origSlug, err)
			return
		}
		http.Redirect(w, r, "/admin/posts", http.StatusSeeOther)
		return
	}

	// New post: slugify the title and create.
	if _, _, err := d.createArticle(in); err != nil {
		d.renderPostEditError(w, in, "", err)
		return
	}
	http.Redirect(w, r, "/admin/posts", http.StatusSeeOther)
}

// renderPostEditError re-renders the editor with the submitted values intact
// and the failure explained, rather than throwing away a long draft on a
// validation error.
func (d *Deps) renderPostEditError(w http.ResponseWriter, in articleInput, slug string, cause error) {
	slog.Warn("admin post save failed", "slug", slug, "err", cause)
	date, err := time.Parse("2006-01-02", in.Date)
	if err != nil {
		date = time.Now()
	}
	w.WriteHeader(http.StatusBadRequest)
	d.adminTmpls.render(w, "post-edit", d.adminData("Edit Post", map[string]any{
		"Error": cause.Error(),
		"Article": build.Article{
			Slug: slug, Title: in.Title, Date: date, Description: in.Description,
			Tags: in.Tags, Draft: in.Draft, Body: in.Body,
		},
	}))
}

// adminPostPreview renders the posted markdown body to HTML for the editor's
// "Preview" tab. It does not touch disk.
func (d *Deps) adminPostPreview(w http.ResponseWriter, r *http.Request) {
	if !parseAdminForm(w, r) {
		return
	}
	html, err := markdown.Render(r.FormValue("body"))
	if err != nil {
		http.Error(w, "render error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(html))
}

func (d *Deps) adminPostDelete(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if _, err := d.deleteArticle(slug); err != nil {
		// Non-fatal in the admin UI: log and still bounce back to the list so the
		// user isn't stuck on an error page.
		slog.Error("admin post delete", "slug", slug, "err", err)
	}
	http.Redirect(w, r, "/admin/posts", http.StatusSeeOther)
}

func (d *Deps) adminLinksList(w http.ResponseWriter, r *http.Request) {
	d.renderLinksList(w, "")
}

func (d *Deps) renderLinksList(w http.ResponseWriter, errMsg string) {
	if !d.adminReady(w) {
		return
	}
	links, err := d.Store.ListLinks(500)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	data := map[string]any{
		"Links": links,
		"Feeds": d.Site().RSSFeeds,
		// Shown next to the feed list so the operator knows how long until
		// the next automatic poll.
		"PollInterval": d.Cfg.RSSInterval.String(),
		// Drives the display-style checkbox below the link list.
		"ShowLinkNotes": d.Site().ShowLinkNotes(),
	}
	if errMsg != "" {
		data["Error"] = errMsg
		w.WriteHeader(http.StatusBadRequest)
	}
	d.adminTmpls.render(w, "links-list", d.adminData("Links", data))
}

func (d *Deps) adminLinkAdd(w http.ResponseWriter, r *http.Request) {
	if !parseAdminForm(w, r) {
		return
	}
	label := strings.TrimSpace(r.FormValue("label"))
	href := strings.TrimSpace(r.FormValue("href"))
	if label == "" || href == "" {
		d.renderLinksList(w, "Both a label and a URL are required.")
		return
	}
	if _, err := d.Store.AddLink(store.Link{
		Label: label, Href: href, Note: strings.TrimSpace(r.FormValue("note")),
		Source: "manual", SortDate: time.Now().Unix(),
	}); err != nil {
		// A rejected scheme or a duplicate href is the operator's input
		// problem, not a server fault — say so in the page instead of
		// replacing the admin UI with a 500.
		slog.Warn("admin link add", "err", err)
		d.renderLinksList(w, linkErrorMessage(err))
		return
	}
	if err := d.Engine.Rebuild(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	http.Redirect(w, r, "/admin/links", http.StatusSeeOther)
}

// linkErrorMessage turns a store error into something an operator can act on.
func linkErrorMessage(err error) string {
	switch {
	case strings.Contains(err.Error(), "disallowed scheme"):
		return "That URL scheme isn't allowed. Use http://, https://, or mailto:."
	case strings.Contains(err.Error(), "UNIQUE"), strings.Contains(err.Error(), "already"):
		return "That URL is already in the links list."
	default:
		return "Could not add the link."
	}
}

func (d *Deps) adminLinkDelete(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	removed := false
	if id > 0 {
		var err error
		removed, err = d.Store.RemoveLink(id)
		if err != nil {
			slog.Error("admin link delete", "id", id, "err", err)
		}
	}
	// A no-op delete (bad id, RSS link) must not trigger a full re-render.
	if removed {
		if err := d.Engine.Rebuild(); err != nil {
			slog.Error("rebuild after link delete", "err", err)
		}
	}
	http.Redirect(w, r, "/admin/links", http.StatusSeeOther)
}

// adminLinksStyle sets whether /links renders each entry's summary line, from
// the checkbox on the Links page. It writes the same site.yml `links_style`
// key as the dropdown on the Site page, so the two controls stay in agreement
// by construction rather than by being kept in sync.
//
// An unchecked box submits no field at all, so a missing value means "show the
// notes". That reading is only safe because this form carries exactly one
// control: there is no way to submit it without having made a deliberate
// choice about this setting. The Site page's dropdown needs the opposite
// default (anything but an explicit "minimal" means full) because it posts
// alongside several other fields.
func (d *Deps) adminLinksStyle(w http.ResponseWriter, r *http.Request) {
	if !parseAdminForm(w, r) {
		return
	}
	site, err := siteconfig.Load(d.Cfg.SiteYAMLPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if r.FormValue("hide_notes") != "" {
		site.LinksStyle = siteconfig.LinksStyleMinimal
	} else {
		site.LinksStyle = siteconfig.LinksStyleFull
	}
	if err := siteconfig.Save(d.Cfg.SiteYAMLPath, site); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := d.reloadSite(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	// /links is a static page, so the setting only becomes visible once the
	// site is rebuilt.
	if err := d.Engine.Rebuild(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	http.Redirect(w, r, "/admin/links", http.StatusSeeOther)
}

func (d *Deps) adminSiteEdit(w http.ResponseWriter, r *http.Request) {
	d.renderSiteEdit(w, "")
}

// renderSiteEdit draws the Site page, optionally with an error banner. The
// favicon upload posts here, so it needs a way to report a rejected file
// without losing the rest of the page.
func (d *Deps) renderSiteEdit(w http.ResponseWriter, errMsg string) {
	d.renderSiteEditWith(w, d.Site(), errMsg)
}

// renderSiteEditWith draws the page from a given config rather than the live
// one, so a save rejected by validation comes back with what was typed still in
// the fields instead of silently reverting to what is on disk.
func (d *Deps) renderSiteEditWith(w http.ResponseWriter, site *siteconfig.Config, errMsg string) {
	if !d.adminReady(w) {
		return
	}
	data := map[string]any{
		"Site": site,
		// Offered as a dropdown in the social-link form: an unknown icon
		// renders as nothing, so free text would let a typo produce an
		// invisible link.
		"Icons": build.SocialIconNames(),
	}
	if errMsg != "" {
		data["Error"] = errMsg
		w.WriteHeader(http.StatusBadRequest)
	}
	d.adminTmpls.render(w, "site-edit", d.adminData("Site Config", data))
}

// adminSiteSave edits only the scalar/textarea fields exposed in the site form
// (title, description, bio). The richer sections (nav/social/rss_feeds) are
// managed via the REST API; mutating them through a freeform textarea would be
// error-prone.
//
// As with updateSiteSection, we apply the edits to a freshly-loaded copy and
// publish it via ReloadSite rather than mutating the live shared pointer,
// which would race the engine's reads during Rebuild.
func (d *Deps) adminSiteSave(w http.ResponseWriter, r *http.Request) {
	if !parseAdminForm(w, r) {
		return
	}
	site, err := siteconfig.Load(d.Cfg.SiteYAMLPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	site.Title = strings.TrimSpace(r.FormValue("title"))
	site.Description = strings.TrimSpace(r.FormValue("description"))
	site.Bio = splitLines(r.FormValue("bio"))
	// Anything other than the explicit "minimal" means the full style, so a
	// missing or unexpected value can't turn the summaries off by accident.
	if r.FormValue("links_style") == siteconfig.LinksStyleMinimal {
		site.LinksStyle = siteconfig.LinksStyleMinimal
	} else {
		site.LinksStyle = siteconfig.LinksStyleFull
	}
	analytics := siteconfig.Analytics{
		ScriptURL: strings.TrimSpace(r.FormValue("analytics_script")),
		Data:      parseAttrLines(r.FormValue("analytics_data")),
	}
	// Refused rather than saved-and-ignored: a tracker that silently does not
	// load looks identical to one that is working but has no visitors, and this
	// URL also widens the site's script-src.
	site.Analytics = analytics
	if err := siteconfig.ValidateAnalytics(analytics); err != nil {
		d.renderSiteEditWith(w, site, err.Error())
		return
	}
	if err := siteconfig.Save(d.Cfg.SiteYAMLPath, site); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := d.reloadSite(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := d.Engine.Rebuild(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	http.Redirect(w, r, "/admin/site", http.StatusSeeOther)
}

func (d *Deps) adminRegenerate(w http.ResponseWriter, r *http.Request) {
	if err := d.Engine.Rebuild(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

// parseAttrLines reads a textarea of "name: value" lines into a map, for the
// analytics data-* attributes.
//
// One per line with the first colon separating them, so a value containing a
// colon (a URL, most likely) needs no escaping. Blank lines and lines without a
// colon are dropped rather than rejected: the field is free text and a stray
// newline should not fail a save.
func parseAttrLines(s string) map[string]string {
	out := map[string]string{}
	for _, line := range splitLines(s) {
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		// "data-website-id" and "website-id" both mean the same thing to
		// somebody pasting from a provider's docs.
		name = strings.TrimPrefix(name, "data-")
		if name == "" {
			continue
		}
		out[name] = strings.TrimSpace(value)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// splitLines breaks a textarea blob into trimmed non-empty lines. Used for the
// bio field, which the front page renders one paragraph per line.
func splitLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	parts := strings.Split(s, "\n")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// postsDir returns the directory holding article source files.
func (d *Deps) postsDir() string {
	return filepath.Join(d.Cfg.ContentDir, "posts")
}
