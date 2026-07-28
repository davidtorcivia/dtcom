// Package build is the static site rebuild engine.
package build

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/adrg/frontmatter"
)

type Article struct {
	Slug        string    `yaml:"-"`
	Title       string    `yaml:"title"`
	Date        time.Time `yaml:"date"`
	Description string    `yaml:"description"`
	Tags        []string  `yaml:"tags"`
	Cover       string    `yaml:"cover"`
	Draft       bool      `yaml:"draft"`
	Body        string    `yaml:"-"` // markdown body, no frontmatter

	// SourcePath is the absolute path to the .md file.
	SourcePath string `yaml:"-"`
}

// LoadArticles reads every *.md file in dir, parses frontmatter, and returns
// them sorted by date desc. Drafts are included (filtered at render time).
func LoadArticles(dir string) ([]Article, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read posts dir: %w", err)
	}
	var arts []Article
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", e.Name(), err)
		}
		var a Article
		rest, err := frontmatter.Parse(strings.NewReader(string(raw)), &a)
		if err != nil {
			return nil, fmt.Errorf("parse frontmatter %s: %w", e.Name(), err)
		}
		// Trim leading blank-line separator left behind after the closing
		// frontmatter delimiter. Leading blank lines are insignificant in
		// markdown; this yields a clean body starting at the first content line.
		a.Body = strings.TrimLeft(string(rest), "\r\n")
		a.SourcePath = path
		if a.Slug == "" {
			a.Slug = slugFromFilename(e.Name())
		}
		arts = append(arts, a)
	}
	sort.Slice(arts, func(i, j int) bool { return arts[i].Date.After(arts[j].Date) })
	return arts, nil
}

// slugFromFilename turns "2026-01-31-schedlock.md" into "schedlock".
func slugFromFilename(name string) string {
	name = strings.TrimSuffix(name, ".md")
	// strip leading date "YYYY-MM-DD-"
	if len(name) >= 11 && name[4] == '-' && name[7] == '-' && name[10] == '-' {
		name = name[11:]
	}
	return name
}
