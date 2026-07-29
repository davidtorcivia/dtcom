package markdown

import (
	"strings"
	"testing"
)

// lossless is an image of the kind that gets WebP renditions: the resolver
// answers for one URL and nothing else.
func lossless(src string) (*ImageInfo, bool) {
	if src != "/images/abc.png" {
		return nil, false
	}
	return &ImageInfo{
		Master:     "/images/abc.png",
		MasterWebP: "/images/abc.webp",
		Width:      2000,
		Height:     1200,
		WebP: []Rendition{
			{Width: 480, URL: "/images/abc.w480.webp"},
			{Width: 1080, URL: "/images/abc.w1080.webp"},
			{Width: 2000, URL: "/images/abc.webp"},
		},
		Fallback: []Rendition{
			{Width: 480, URL: "/images/abc.w480.png"},
			{Width: 1080, URL: "/images/abc.w1080.png"},
			{Width: 2000, URL: "/images/abc.png"},
		},
	}, true
}

// photo has no WebP renditions, which is the JPEG path.
func photo(src string) (*ImageInfo, bool) {
	if src != "/images/pic.jpg" {
		return nil, false
	}
	return &ImageInfo{
		Master: "/images/pic.jpg",
		Width:  1600,
		Height: 1000,
		Fallback: []Rendition{
			{Width: 480, URL: "/images/pic.w480.jpg"},
			{Width: 1080, URL: "/images/pic.w1080.jpg"},
			{Width: 1600, URL: "/images/pic.jpg"},
		},
	}, true
}

func mustRender(t *testing.T, src string, res ImageResolver) string {
	t.Helper()
	out, err := RenderWith(src, res)
	if err != nil {
		t.Fatalf("RenderWith: %v", err)
	}
	return out
}

func TestResponsiveWrapsLosslessImages(t *testing.T) {
	out := mustRender(t, "![A chart](/images/abc.png)", lossless)

	for _, want := range []string{
		`<picture>`,
		`<source type="image/webp" srcset="/images/abc.w480.webp 480w, /images/abc.w1080.webp 1080w, /images/abc.webp 2000w"`,
		`srcset="/images/abc.w480.png 480w, /images/abc.w1080.png 1080w, /images/abc.png 2000w"`,
		`src="/images/abc.w1080.png"`, // the column width, for a browser with no srcset
		`width="2000" height="1200"`,  // reserves the space before it loads
		`data-full="/images/abc.png"`,
		`data-full-webp="/images/abc.webp"`,
		`data-full-w="2000"`,
		`decoding="async"`,
		`fetchpriority="high"`, // first picture in the post
		`sizes="(max-width: 640px)`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s\ngot: %s", want, out)
		}
	}
	// The figure is still a figure: this pass runs after captioning, and a
	// <picture> wrapper before it would have hidden the image from it.
	if !strings.Contains(out, "<figure>") || !strings.Contains(out, "<figcaption>A chart</figcaption>") {
		t.Errorf("figure lost: %s", out)
	}
	// Exactly one src survives: the one this pass wrote. goldmark's original
	// is stripped rather than left as a duplicate.
	if n := strings.Count(out, ` src="`); n != 1 {
		t.Errorf("expected one src attribute, got %d: %s", n, out)
	}
}

func TestResponsivePhotographStaysAnImg(t *testing.T) {
	out := mustRender(t, "![A photo](/images/pic.jpg)", photo)
	if strings.Contains(out, "<picture") || strings.Contains(out, "<source") {
		t.Errorf("photograph should not be wrapped: %s", out)
	}
	if !strings.Contains(out, `srcset="/images/pic.w480.jpg 480w, /images/pic.w1080.jpg 1080w, /images/pic.jpg 1600w"`) {
		t.Errorf("missing srcset: %s", out)
	}
	if !strings.Contains(out, `data-full="/images/pic.jpg"`) {
		t.Errorf("missing data-full: %s", out)
	}
	if strings.Contains(out, "data-full-webp") {
		t.Errorf("photograph should have no WebP master: %s", out)
	}
}

func TestResponsiveLeavesUnknownImagesAlone(t *testing.T) {
	src := "![Remote](https://example.com/x.png)\n\n![Missing](/images/gone.png)"
	out := mustRender(t, src, lossless)
	if strings.Contains(out, "srcset") || strings.Contains(out, "data-full") {
		t.Errorf("rewrote an image it could not resolve: %s", out)
	}
	// And Render, which has no resolver at all, changes nothing.
	plain, err := Render("![A chart](/images/abc.png)")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plain, "srcset") {
		t.Errorf("Render should not add renditions: %s", plain)
	}
}

// TestResponsiveThemedPair is the interaction most likely to break quietly:
// a light/dark pair is one figure holding two images, only one of which is
// displayed, and the class that decides which has to end up on the element
// that gets a box — the <picture>, once there is one.
func TestResponsiveThemedPair(t *testing.T) {
	pair := func(src string) (*ImageInfo, bool) {
		switch src {
		case "/images/fig-light.png", "/images/fig-dark.png":
			base := strings.TrimSuffix(strings.TrimPrefix(src, "/images/"), ".png")
			return &ImageInfo{
				Master:     src,
				MasterWebP: "/images/" + base + ".webp",
				Width:      1400,
				Height:     800,
				WebP:       []Rendition{{Width: 480, URL: "/images/" + base + ".w480.webp"}},
				Fallback:   []Rendition{{Width: 480, URL: "/images/" + base + ".w480.png"}},
			}, true
		}
		return nil, false
	}
	out := mustRender(t,
		"![The transfer function](/images/fig-light.png#light)\n![The transfer function](/images/fig-dark.png#dark)",
		pair)

	if !strings.Contains(out, "<figure>") ||
		!strings.Contains(out, "<figcaption>The transfer function</figcaption>") {
		t.Fatalf("the pair stopped being one figure: %s", out)
	}
	if !strings.Contains(out, `<picture class="theme-light">`) ||
		!strings.Contains(out, `<picture class="theme-dark">`) {
		t.Errorf("theme class did not move to the picture: %s", out)
	}
	if strings.Contains(out, `<img class="theme-`) {
		t.Errorf("theme class left on the img, which has no box to hide: %s", out)
	}
	// The marker must not survive into the URL, or every request carries it.
	if strings.Contains(out, "#light") || strings.Contains(out, "#dark") {
		t.Errorf("theme marker leaked into a URL: %s", out)
	}
}

// TestResponsiveLoadingPriority: the first picture is what a reader arrives
// on, the rest are below the fold.
func TestResponsiveLoadingPriority(t *testing.T) {
	out := mustRender(t, "![One](/images/abc.png)\n\ntext\n\n![Two](/images/abc.png)", lossless)
	if n := strings.Count(out, `fetchpriority="high"`); n != 1 {
		t.Errorf("fetchpriority on %d images, want 1: %s", n, out)
	}
	if n := strings.Count(out, `loading="lazy"`); n != 1 {
		t.Errorf("lazy on %d images, want 1: %s", n, out)
	}
	if strings.Index(out, `fetchpriority="high"`) > strings.Index(out, `loading="lazy"`) {
		t.Error("the eager image is not the first one")
	}
}

// TestResponsiveInlineImage: an image mid-sentence is not a figure, but it is
// still worth serving at the right size.
func TestResponsiveInlineImage(t *testing.T) {
	out := mustRender(t, "Some text ![icon](/images/abc.png) more text.", lossless)
	if strings.Contains(out, "<figure>") {
		t.Errorf("inline image promoted to a figure: %s", out)
	}
	if !strings.Contains(out, "srcset=") {
		t.Errorf("inline image got no renditions: %s", out)
	}
}
