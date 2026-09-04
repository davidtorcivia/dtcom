package server

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"davidtorcivia.com/dtcom/internal/build"
)

func (d *Deps) adminMedia(w http.ResponseWriter, _ *http.Request) { d.renderMedia(w, "") }

func (d *Deps) renderMedia(w http.ResponseWriter, message string) {
	images, err := d.listStoredImages("")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	data := map[string]any{"Images": images}
	if message != "" {
		data["Error"] = message
	}
	d.adminTmpls.render(w, "media", d.adminData("Media", data))
}

func (d *Deps) adminMediaUpload(w http.ResponseWriter, r *http.Request) {
	if _, err := d.storeUploadedImage(w, r); err != nil {
		d.renderMedia(w, err.Error())
		return
	}
	http.Redirect(w, r, "/admin/media", http.StatusSeeOther)
}

func (d *Deps) adminMediaDelete(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if filepath.Base(name) != name || !build.IsMasterImage(name) {
		d.renderMedia(w, "Invalid image name.")
		return
	}
	d.siteMu.Lock()
	defer d.siteMu.Unlock()
	d.postMu.Lock()
	defer d.postMu.Unlock()
	url := "/images/" + name
	images, err := d.listStoredImages("")
	if err != nil {
		d.renderMedia(w, err.Error())
		return
	}
	for _, image := range images {
		if image.URL == url && (len(image.UsedBy) > 0 || d.Site().Favicon == url) {
			d.renderMedia(w, fmt.Sprintf("%s is still used and was not deleted.", name))
			return
		}
	}
	base := strings.TrimSuffix(name, filepath.Ext(name))
	entries, err := os.ReadDir(d.Cfg.ImagesDir)
	if err != nil {
		d.renderMedia(w, err.Error())
		return
	}
	for _, entry := range entries {
		candidate := entry.Name()
		if !entry.IsDir() && (candidate == name || strings.HasPrefix(candidate, base+".")) {
			if err := os.Remove(filepath.Join(d.Cfg.ImagesDir, candidate)); err != nil && !os.IsNotExist(err) {
				d.renderMedia(w, err.Error())
				return
			}
		}
	}
	if d.Engine != nil && d.Engine.Images() != nil {
		d.Engine.Images().Refresh()
	}
	http.Redirect(w, r, "/admin/media", http.StatusSeeOther)
}
