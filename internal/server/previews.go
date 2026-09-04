package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

func (d *Deps) previewURL(slug string, expires time.Time) string {
	exp := strconv.FormatInt(expires.Unix(), 10)
	sig := d.previewSignature(slug, exp)
	return fmt.Sprintf("/preview/%s?expires=%s&signature=%s", slug, exp, sig)
}

func (d *Deps) previewSignature(slug, expires string) string {
	mac := hmac.New(sha256.New, []byte(d.Cfg.SessionKey))
	mac.Write([]byte(slug + "\x00" + expires))
	return hex.EncodeToString(mac.Sum(nil))
}

func (d *Deps) handleSignedPreview(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	exp := r.URL.Query().Get("expires")
	unix, err := strconv.ParseInt(exp, 10, 64)
	want := d.previewSignature(slug, exp)
	if err != nil || time.Now().Unix() > unix || !hmac.Equal([]byte(want), []byte(r.URL.Query().Get("signature"))) {
		d.handleNotFound(w, r)
		return
	}
	a, err := d.findArticleBySlug(slug)
	if err != nil || a == nil {
		d.handleNotFound(w, r)
		return
	}
	body, err := d.Engine.Preview(*a)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "private, no-store")
	_, _ = w.Write(body)
}
