package server

import (
	"net/http"
	"strconv"
)

func (d *Deps) handleWebmention(w http.ResponseWriter, r *http.Request) {
	if d.Webmentions == nil {
		writeError(w, http.StatusServiceUnavailable, nil)
		return
	}
	if !d.limits.mentions.Allow(d.clientIP(r)) {
		writeError(w, http.StatusTooManyRequests, nil)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := r.ParseForm(); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	id, err := d.Webmentions.Receive(r.Context(), r.FormValue("source"), r.FormValue("target"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	w.Header().Set("Location", "/admin/mentions#mention-"+strconv.FormatInt(id, 10))
	w.WriteHeader(http.StatusAccepted)
}

func (d *Deps) adminMentions(w http.ResponseWriter, _ *http.Request) { d.renderMentions(w, "") }

func (d *Deps) renderMentions(w http.ResponseWriter, message string) {
	items, err := d.Store.ListWebmentions(200)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	data := map[string]any{"Items": items}
	if message != "" {
		data["Error"] = message
	}
	d.adminTmpls.render(w, "mentions", d.adminData("Webmentions", data))
}

func (d *Deps) adminMentionModerate(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	status := r.PathValue("status")
	if err != nil || status != "approved" && status != "rejected" {
		d.renderMentions(w, "Invalid moderation request.")
		return
	}
	ok, err := d.Store.ModerateWebmention(id, status)
	if err != nil || !ok {
		d.renderMentions(w, "That verified mention is no longer available.")
		return
	}
	http.Redirect(w, r, "/admin/mentions", http.StatusSeeOther)
}
