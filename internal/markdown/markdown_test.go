package markdown

import (
	"strings"
	"testing"
)

func TestRenderBasics(t *testing.T) {
	in := "# Title\n\nA _bold_ **strong** paragraph with `code`.\n\n> Quote\n"
	html, err := Render(in)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	wantSubstrings := []string{"<h1", "<em>bold</em>", "<strong>strong</strong>", "<code>code</code>", "<blockquote>"}
	for _, s := range wantSubstrings {
		if !strings.Contains(html, s) {
			t.Errorf("output missing %q\n%s", s, html)
		}
	}
}

func TestRenderHighlight(t *testing.T) {
	in := "Text with ==highlighted== words."
	html, err := Render(in)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(html, "<mark>highlighted</mark>") {
		t.Errorf("expected <mark>, got:\n%s", html)
	}
}

func TestRenderCodeBlockLang(t *testing.T) {
	in := "```go\npackage main\n```\n"
	html, err := Render(in)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(html, `class="language-go"`) {
		t.Errorf("expected language-go class, got:\n%s", html)
	}
}

func TestRenderTable(t *testing.T) {
	in := "| A | B |\n|---|---|\n| 1 | 2 |\n"
	html, err := Render(in)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(html, "<table>") {
		t.Errorf("expected <table>, got:\n%s", html)
	}
}
