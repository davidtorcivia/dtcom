package server

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMediaLibraryDeletesUnusedImage(t *testing.T) {
	d := newTestDepsWithAdmin(t)
	if rec := d.adminUpload(t, "/admin/media/upload", "file", "library.png", testPNG(t)); rec.Code != http.StatusSeeOther {
		t.Fatalf("upload = %d: %s", rec.Code, rec.Body.String())
	}
	images, err := d.deps.listStoredImages("")
	if err != nil || len(images) != 1 {
		t.Fatalf("media list = %+v, %v", images, err)
	}
	name := filepath.Base(images[0].URL)
	if rec := d.adminPost(t, "/admin/media/"+name+"/delete", nil); rec.Code != http.StatusSeeOther {
		t.Fatalf("delete = %d: %s", rec.Code, rec.Body.String())
	}
	entries, err := os.ReadDir(d.deps.Cfg.ImagesDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), strings.TrimSuffix(name, filepath.Ext(name))) {
			t.Fatalf("image artifact survived deletion: %s", entry.Name())
		}
	}
}
