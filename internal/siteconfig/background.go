package siteconfig

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
)

// Background keeps full, independent settings for each viewport and theme.
// Nil on older sites means disabled and incurs no public asset requests.
type Background struct {
	Enabled  bool                         `json:"enabled" yaml:"enabled"`
	Profiles map[string]BackgroundProfile `json:"profiles" yaml:"profiles"`
	Presets  []BackgroundPreset           `json:"presets" yaml:"presets"`
}
type BackgroundPreset struct {
	Name    string            `json:"name" yaml:"name"`
	Profile BackgroundProfile `json:"profile" yaml:"profile"`
}
type BackgroundProfile struct {
	Mode         int      `json:"mode" yaml:"mode"`
	Palette      string   `json:"palette" yaml:"palette"`
	Colors       []string `json:"colors" yaml:"colors"`
	Presence     float64  `json:"presence" yaml:"presence"`
	Scale        float64  `json:"scale" yaml:"scale"`
	Detail       float64  `json:"detail" yaml:"detail"`
	Speed        float64  `json:"speed" yaml:"speed"`
	Protect      float64  `json:"protect" yaml:"protect"`
	Grain        float64  `json:"grain" yaml:"grain"`
	Filaments    float64  `json:"filaments" yaml:"filaments"`
	Lines        float64  `json:"lines" yaml:"lines"`
	LineSize     float64  `json:"lineSize" yaml:"line_size"`
	FiberWidth   float64  `json:"fiberWidth" yaml:"fiber_width"`
	LineSpacing  float64  `json:"lineSpacing" yaml:"line_spacing"`
	FiberSpacing float64  `json:"fiberSpacing" yaml:"fiber_spacing"`
	Quality      float64  `json:"quality" yaml:"quality"`
}

func (b *Background) JSON() string {
	if b == nil {
		return "null"
	}
	v, _ := json.Marshal(b)
	return string(v)
}

var backgroundColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

func ValidateBackground(b *Background) error {
	if b == nil {
		return nil
	}
	if len(b.Profiles) != 4 {
		return fmt.Errorf("all four background contexts are required")
	}
	for _, key := range []string{"mobileLight", "mobileDark", "desktopLight", "desktopDark"} {
		p, ok := b.Profiles[key]
		if !ok {
			return fmt.Errorf("missing background context %s", key)
		}
		if err := validateBackgroundProfile(p); err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
	}
	if len(b.Presets) > 20 {
		return fmt.Errorf("at most 20 custom presets are allowed")
	}
	names := map[string]bool{}
	for _, p := range b.Presets {
		if len(p.Name) == 0 || len(p.Name) > 60 || names[p.Name] {
			return fmt.Errorf("custom preset names must be unique and 1–60 bytes")
		}
		names[p.Name] = true
		if err := validateBackgroundProfile(p.Profile); err != nil {
			return err
		}
	}
	return nil
}
func validateBackgroundProfile(p BackgroundProfile) error {
	if p.Mode != 1 && p.Mode != 3 && p.Mode != 4 {
		return fmt.Errorf("unknown shader")
	}
	if len(p.Palette) > 32 {
		return fmt.Errorf("invalid palette")
	}
	if len(p.Colors) != 3 {
		return fmt.Errorf("three colors are required")
	}
	for _, c := range p.Colors {
		if !backgroundColor.MatchString(c) {
			return fmt.Errorf("invalid color")
		}
	}
	for _, v := range []struct {
		name            string
		value, min, max float64
	}{
		{"presence", p.Presence, 0, 100}, {"scale", p.Scale, 45, 200}, {"structure", p.Detail, 0, 100}, {"motion", p.Speed, 0, 100}, {"reading space", p.Protect, 0, 100}, {"grain", p.Grain, 0, 100},
		{"filaments", p.Filaments, 16, 320}, {"lines", p.Lines, 8, 100}, {"line width", p.LineSize, 25, 250}, {"fiber width", p.FiberWidth, 25, 400}, {"line spacing", p.LineSpacing, 40, 250}, {"fiber spacing", p.FiberSpacing, 40, 250},
	} {
		if math.IsNaN(v.value) || math.IsInf(v.value, 0) || v.value < v.min || v.value > v.max {
			return fmt.Errorf("%s must be between %g and %g", v.name, v.min, v.max)
		}
	}
	if p.Quality != .65 && p.Quality != 1 && p.Quality != 1.5 {
		return fmt.Errorf("invalid quality")
	}
	return nil
}

// PublicJSON omits the author's preset library from every visitor response.
func (b *Background) PublicJSON() string {
	if b == nil {
		return "null"
	}
	v, _ := json.Marshal(struct {
		Enabled  bool                         `json:"enabled"`
		Profiles map[string]BackgroundProfile `json:"profiles"`
	}{b.Enabled, b.Profiles})
	return string(v)
}
