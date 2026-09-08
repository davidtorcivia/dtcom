package server

import (
	"davidtorcivia.com/dtcom/internal/siteconfig"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
)

func backgroundFixture() *siteconfig.Background {
	b := &siteconfig.Background{Enabled: true, Profiles: map[string]siteconfig.BackgroundProfile{}}
	for i, k := range []string{"desktopLight", "desktopDark", "mobileLight", "mobileDark"} {
		b.Profiles[k] = siteconfig.BackgroundProfile{Mode: 3, Palette: "signature", Colors: []string{"#64859f", "#be927a", "#b7a4bd"}, Presence: 60, Scale: 100, Detail: 42, Speed: 18, Protect: float64(50 + i*10), Grain: 6, Filaments: 80, Lines: 28, LineSize: 100, FiberWidth: 100, LineSpacing: 100, FiberSpacing: 100, Quality: 1}
	}
	b.Presets = []siteconfig.BackgroundPreset{{Name: "My glass", Profile: b.Profiles["mobileDark"]}}
	return b
}
func TestBackgroundSaveAndRender(t *testing.T) {
	d := newTestDepsWithAdmin(t)
	session := httptest.NewRecorder()
	d.deps.Auth.SetSession(session, "admin")
	cookie := session.Result().Cookies()[0]
	send := func(method, path, body string, auth bool, origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if auth {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		d.mux.ServeHTTP(w, r)
		return w
	}
	initial, _ := os.ReadFile(d.deps.Engine.PublicDir() + "/index.html")
	if strings.Contains(string(initial), "dt-background") {
		t.Fatal("disabled background emits public assets")
	}
	if w := send("GET", "/admin/site/background", "", true, ""); w.Code != 200 || !strings.Contains(w.Body.String(), "background-context") {
		t.Fatalf("editor: %d %s", w.Code, w.Body.String())
	}
	b := backgroundFixture()
	raw, _ := json.Marshal(b)
	if w := send("POST", "/admin/site/background", string(raw), false, ""); w.Code != 303 {
		t.Fatalf("unauth: %d", w.Code)
	}
	if w := send("POST", "/admin/site/background", string(raw), true, "https://evil.example"); w.Code != 403 {
		t.Fatalf("cross origin: %d", w.Code)
	}
	if w := send("POST", "/admin/site/background", string(raw), true, ""); w.Code != 200 {
		t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}
	if w := send("GET", "/admin/site/background", "", true, ""); w.Code != 200 || strings.Contains(w.Body.String(), "#ZgotmplZ") {
		t.Fatalf("saved editor config escaped incorrectly: %s", w.Body.String())
	}
	loaded, err := siteconfig.Load(d.deps.Cfg.SiteYAMLPath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Background.JSON() != b.JSON() {
		t.Fatal("profiles/presets changed in YAML round trip")
	}
	body, _ := os.ReadFile(d.deps.Engine.PublicDir() + "/index.html")
	if !strings.Contains(string(body), "dt-background") || !strings.Contains(string(body), "background.js") {
		t.Fatal("save did not rebuild public page")
	}
	if w := saveSite(t, d, url.Values{"title": {"Unrelated edit"}}); w.Code != 303 {
		t.Fatal(w.Code)
	}
	if d.deps.Site().Background.JSON() != b.JSON() {
		t.Fatal("site form discarded backgrounds")
	}
	for _, path := range []string{"/admin/site/background/preview", "/admin/site/background/preview?article=hello"} {
		w := send("GET", path, "", true, "")
		if w.Code != 200 || !strings.Contains(w.Body.String(), "site-container") || !strings.Contains(w.Header().Get("Content-Security-Policy"), "frame-ancestors 'self'") {
			t.Fatalf("preview: %d %s", w.Code, w.Body.String())
		}
	}
	if w := send("GET", "/admin/site/background/preview", "", false, ""); w.Code != 303 || w.Header().Get("X-Frame-Options") != "DENY" {
		t.Fatal("preview must require auth")
	}
	old := d.deps.Site().Background.JSON()
	bad := b.Profiles["mobileDark"]
	bad.Filaments = 999999
	b.Profiles["mobileDark"] = bad
	raw, _ = json.Marshal(b)
	if w := send("POST", "/admin/site/background", string(raw), true, ""); w.Code != http.StatusBadRequest {
		t.Fatal(w.Code)
	}
	if d.deps.Site().Background.JSON() != old {
		t.Fatal("invalid save mutated configuration")
	}
	b = backgroundFixture()
	b.Enabled = false
	raw, _ = json.Marshal(b)
	send("POST", "/admin/site/background", string(raw), true, "")
	body, _ = os.ReadFile(d.deps.Engine.PublicDir() + "/index.html")
	if strings.Contains(string(body), "dt-background") {
		t.Fatal("disable still loads renderer")
	}
}
