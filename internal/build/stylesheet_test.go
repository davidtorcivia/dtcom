package build

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The nav links in the site header contain nothing but their label text —
// there is no icon element to fall back to, and nav entries are author-defined
// free text, so there is no icon vocabulary that could provide one.
//
// The stylesheet used to hide .nav-text-label under 640px in favour of a
// .nav-icon-label that no template has ever rendered. The result was that
// Search and Links were zero-width invisible anchors on every phone, which
// looked fine on every desktop and shipped to production before anyone opened
// the site on a handset.
//
// This guards the invariant rather than the styling: the label may be resized
// or restyled freely, but it must not be removed from the layout.
func TestNavLabelIsNeverHidden(t *testing.T) {
	path := filepath.Join("..", "..", "static", "style.css")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Comments are stripped first: this is a check on the rules that actually
	// apply, and the stylesheet explains in prose why these rules are absent.
	// Without this the test matches its own documentation.
	css := regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(string(raw), "")

	// Any rule whose selector mentions .nav-text-label and whose body removes
	// it from the layout. Tolerates whitespace and extra declarations.
	hides := regexp.MustCompile(`(?s)\.nav-text-label[^{]*\{[^}]*display\s*:\s*none`)
	if loc := hides.FindStringIndex(css); loc != nil {
		t.Errorf("style.css hides .nav-text-label, which is the entire visible content of a nav link:\n\t%s",
			strings.TrimSpace(css[loc[0]:loc[1]]))
	}

	// The icon element the old rule deferred to does not exist. If it is ever
	// reintroduced in CSS, a template has to render it too.
	if strings.Contains(css, "nav-icon-label") {
		t.Error("style.css references .nav-icon-label, but no template renders that element")
	}
}
