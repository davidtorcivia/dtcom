package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// solidPNG is a real image, because the store decodes and re-encodes what it is
// given — a handful of fake bytes would be rejected before reaching any of the
// behaviour under test.
func solidPNG(t *testing.T, c color.RGBA) []byte {
	t.Helper()
	im := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for x := 0; x < 8; x++ {
		for y := 0; y < 8; y++ {
			im.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, im); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestAddImageFromBase64(t *testing.T) {
	d := newTestDeps(t)
	raw := solidPNG(t, color.RGBA{R: 240, G: 240, B: 235, A: 255})

	got, err := imageBytesFrom(context.Background(), addImageArgs{
		Data: base64.StdEncoding.EncodeToString(raw),
	})
	if err != nil {
		t.Fatal(err)
	}
	url, err := d.deps.storeImageBytes(got)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(url, "/images/") {
		t.Fatalf("url = %q", url)
	}
	name := strings.TrimPrefix(url, "/images/")
	if _, err := os.Stat(filepath.Join(d.deps.Cfg.ImagesDir, name)); err != nil {
		t.Errorf("stored file missing: %v", err)
	}

	// A data: URI's payload and the URL-safe alphabet both have to decode, so a
	// caller does not have to know which encoder this end expects.
	if _, err := imageBytesFrom(context.Background(), addImageArgs{
		Data: "data:image/png;base64," + base64.StdEncoding.EncodeToString(raw),
	}); err != nil {
		t.Errorf("data: URI payload rejected: %v", err)
	}
	if _, err := imageBytesFrom(context.Background(), addImageArgs{
		Data: base64.RawURLEncoding.EncodeToString(raw),
	}); err != nil {
		t.Errorf("url-safe base64 rejected: %v", err)
	}
}

// Giving both sources is a mistake worth reporting: quietly preferring one
// would leave the caller believing the other had been used.
func TestAddImageSourceIsExclusive(t *testing.T) {
	for _, tc := range []struct {
		name string
		args addImageArgs
		want string
	}{
		{"neither", addImageArgs{}, "either url or data"},
		{"both", addImageArgs{URL: "https://example.com/a.png", Data: "eA=="}, "not both"},
		{"bad base64", addImageArgs{Data: "not base64!!"}, "not valid base64"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := imageBytesFrom(context.Background(), tc.args)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestFetchRemoteImage(t *testing.T) {
	raw := solidPNG(t, color.RGBA{B: 255, A: 255})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(raw)
	}))
	defer srv.Close()

	// The test server is on loopback, which is exactly what the guard exists to
	// refuse — so this asserts the refusal rather than the fetch. The happy
	// path is covered by the scheme and size checks below plus publicIP's own
	// table; reaching the real internet from a test would be worse than not
	// testing it.
	if _, err := fetchRemoteImage(context.Background(), srv.URL+"/a.png"); err == nil {
		t.Error("fetching from loopback was allowed")
	}

	for _, bad := range []string{
		"file:///etc/passwd",
		"gopher://example.com/",
		"ftp://example.com/a.png",
		"https://",
	} {
		if _, err := fetchRemoteImage(context.Background(), bad); err == nil {
			t.Errorf("%s was allowed", bad)
		}
	}
}

// The guard is the whole security story for add_image(url): everything the
// server can reach that the caller cannot goes through here.
func TestPublicIPRejectsInternalAddresses(t *testing.T) {
	internal := []string{
		"127.0.0.1", "::1", // loopback — the admin port is on one of these
		"10.0.0.5", "172.16.3.1", "192.168.1.1", // RFC 1918
		"169.254.169.254",           // cloud metadata, the classic SSRF target
		"100.64.0.1", "100.127.0.1", // carrier-grade NAT / overlay networks
		"fc00::1", "fd12:3456::1", // IPv6 unique local
		"fe80::1",       // IPv6 link local
		"0.0.0.0", "::", // unspecified
		"224.0.0.1", "ff02::1", // multicast
	}
	for _, s := range internal {
		if publicIP(parseIPOrFail(t, s)) {
			t.Errorf("%s was treated as public", s)
		}
	}
	for _, s := range []string{"8.8.8.8", "1.1.1.1", "93.184.216.34", "2606:4700::1111", "100.63.255.255", "100.128.0.1"} {
		if !publicIP(parseIPOrFail(t, s)) {
			t.Errorf("%s was treated as internal", s)
		}
	}
	if publicIP(nil) {
		t.Error("an unparseable address was treated as public")
	}
}

// The markdown a caller pastes has to carry both conventions, because getting
// either wrong fails silently: the post still builds, it just does not render
// as a figure or does not swap with the theme.
func TestImageMarkdown(t *testing.T) {
	for _, tc := range []struct{ alt, theme, want string }{
		{"The transfer function", "", "![The transfer function](/images/a.png)"},
		{"The transfer function", "light", "![The transfer function](/images/a.png#light)"},
		{"", "dark", "![](/images/a.png#dark)"},
		{" spaced ", "DARK", "![spaced](/images/a.png#dark)"},
		{"x", "sideways", "![x](/images/a.png)"}, // unknown theme is simply not tagged
	} {
		if got := imageMarkdown("/images/a.png", tc.alt, tc.theme); got != tc.want {
			t.Errorf("imageMarkdown(%q, %q) = %q, want %q", tc.alt, tc.theme, got, tc.want)
		}
	}
}

// list_images reports which files are already acting as a light/dark pair, read
// off the posts that use them — the stored name is a content hash and says
// nothing about what the picture is for.
func TestListImagesFindsThemePairs(t *testing.T) {
	d := newTestDeps(t)
	if err := os.MkdirAll(d.deps.Cfg.ImagesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	light, err := d.deps.storeImageBytes(solidPNG(t, color.RGBA{R: 250, G: 250, B: 245, A: 255}))
	if err != nil {
		t.Fatal(err)
	}
	dark, err := d.deps.storeImageBytes(solidPNG(t, color.RGBA{R: 10, G: 12, B: 16, A: 255}))
	if err != nil {
		t.Fatal(err)
	}
	loose, err := d.deps.storeImageBytes(solidPNG(t, color.RGBA{G: 200, A: 255}))
	if err != nil {
		t.Fatal(err)
	}

	post := "---\ntitle: \"Figures\"\ndate: 2026-08-19\ndraft: false\n---\n\n" +
		"![The transfer function](" + light + "#light)\n![](" + dark + "#dark)\n\n" +
		"![On its own](" + loose + ")\n"
	if err := os.WriteFile(filepath.Join(d.deps.postsDir(), "2026-08-19-figures.md"), []byte(post), 0o644); err != nil {
		t.Fatal(err)
	}

	imgs, err := d.deps.listStoredImages("")
	if err != nil {
		t.Fatal(err)
	}
	byURL := map[string]storedImage{}
	for _, im := range imgs {
		byURL[im.URL] = im
	}
	if len(byURL) != 3 {
		t.Fatalf("listed %d images, want 3: %+v", len(byURL), imgs)
	}
	if got := byURL[light]; got.Theme != "light" || got.Pair != dark {
		t.Errorf("light half = %+v, want theme=light pair=%s", got, dark)
	}
	if got := byURL[dark]; got.Theme != "dark" || got.Pair != light {
		t.Errorf("dark half = %+v, want theme=dark pair=%s", got, light)
	}
	if got := byURL[loose]; got.Theme != "" || got.Pair != "" {
		t.Errorf("unpaired image reported as %+v, want no theme", got)
	}
	for _, im := range imgs {
		if im.Bytes <= 0 {
			t.Errorf("%s reported %d bytes", im.URL, im.Bytes)
		}
	}
}

// A blank line between the two lines breaks the pair — the renderer only
// collapses images that share a paragraph — so the listing must not claim a
// pairing the reader will never see.
func TestThemePairsNeedAdjacentLines(t *testing.T) {
	paired := "![cap](/images/a.png#light)\n![](/images/b.png#dark)\n"
	if got := themePairsIn(paired); len(got) != 1 || got[0] != [2]string{"/images/a.png", "/images/b.png"} {
		t.Errorf("adjacent lines = %v, want one pair", got)
	}
	// Written dark-first, which the renderer also accepts.
	reversed := "![cap](/images/b.png#dark)\n![](/images/a.png#light)\n"
	if got := themePairsIn(reversed); len(got) != 1 || got[0] != [2]string{"/images/a.png", "/images/b.png"} {
		t.Errorf("reversed = %v, want light first in the pair", got)
	}
	split := "![cap](/images/a.png#light)\n\n![](/images/b.png#dark)\n"
	if got := themePairsIn(split); len(got) != 0 {
		t.Errorf("blank line between = %v, want no pair", got)
	}
	untagged := "![a](/images/a.png)\n![b](/images/b.png)\n"
	if got := themePairsIn(untagged); len(got) != 0 {
		t.Errorf("untagged = %v, want no pair", got)
	}
	twoLights := "![a](/images/a.png#light)\n![b](/images/b.png#light)\n"
	if got := themePairsIn(twoLights); len(got) != 0 {
		t.Errorf("two lights = %v, want no pair", got)
	}
}

// Renditions are generated per reader width and a post must never point at one
// directly, so they have no business in a list a post picks URLs from.
func TestListImagesExcludesRenditions(t *testing.T) {
	d := newTestDeps(t)
	if err := os.MkdirAll(d.deps.Cfg.ImagesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"abc123.png", "abc123.w640.png", "abc123.webp", "abc123.w640.webp", "logo.svg"} {
		if err := os.WriteFile(filepath.Join(d.deps.Cfg.ImagesDir, name), solidPNG(t, color.RGBA{A: 255}), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	imgs, err := d.deps.listStoredImages("")
	if err != nil {
		t.Fatal(err)
	}
	var urls []string
	for _, im := range imgs {
		urls = append(urls, im.URL)
	}
	want := []string{"/images/abc123.png", "/images/logo.svg"}
	if strings.Join(urls, ",") != strings.Join(want, ",") {
		t.Errorf("listed %v, want %v", urls, want)
	}

	filtered, err := d.deps.listStoredImages("logo")
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 1 || filtered[0].URL != "/images/logo.svg" {
		t.Errorf("query filter returned %+v", filtered)
	}
}

func parseIPOrFail(t *testing.T, s string) net.IP {
	t.Helper()
	ip := net.ParseIP(s)
	if ip == nil {
		t.Fatalf("bad test address %q", s)
	}
	return ip
}
