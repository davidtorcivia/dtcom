package server

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"

	"davidtorcivia.com/dtcom/internal/build"
)

// maxImageUpload bounds an uploaded file before any decoding happens.
const maxImageUpload = 20 << 20 // 20 MB

// adminImageUpload accepts a multipart upload from the post editor and returns
// the markdown snippet to paste into the body.
func (d *Deps) adminImageUpload(w http.ResponseWriter, r *http.Request) {
	url, err := d.storeUploadedImage(w, r)
	if err != nil {
		writeError(w, imageErrorStatus(err), err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{
		"url":      url,
		"markdown": fmt.Sprintf("![](%s)", url),
	})
}

// apiUploadImage is the bearer-authenticated equivalent, so an MCP/REST client
// can attach an image to a post it is writing.
func (d *Deps) apiUploadImage(w http.ResponseWriter, r *http.Request) {
	url, err := d.storeUploadedImage(w, r)
	if err != nil {
		writeError(w, imageErrorStatus(err), err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"url": url})
}

func imageErrorStatus(err error) int {
	if errors.Is(err, build.ErrUnsupportedImage) || errors.Is(err, errNoImageField) {
		return http.StatusBadRequest
	}
	if errors.Is(err, errImageTooLarge) {
		return http.StatusRequestEntityTooLarge
	}
	return http.StatusInternalServerError
}

var (
	errNoImageField  = errors.New(`multipart upload is missing the "file" field`)
	errImageTooLarge = errors.New("image exceeds the upload size limit")
)

// storeUploadedImage decodes, normalizes, and writes an uploaded image under
// ImagesDir, returning the URL it is served at.
//
// The stored name is the SHA-256 of the processed bytes, which makes the URL
// content-addressed: uploading the same picture twice reuses one file, and the
// long-lived cache header on /images/ is always correct because a given URL's
// bytes can never change.
func (d *Deps) storeUploadedImage(w http.ResponseWriter, r *http.Request) (string, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxImageUpload)
	defer r.Body.Close()

	// Buffer at most 8 MB in memory; larger uploads spill to a temp file that
	// ParseMultipartForm cleans up.
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return "", errImageTooLarge
		}
		return "", fmt.Errorf("%w: %v", errNoImageField, err)
	}
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()

	file, header, err := r.FormFile("file")
	if err != nil {
		return "", errNoImageField
	}
	defer file.Close()

	img, err := build.ProcessImage(file, build.MaxImageDim)
	if err != nil {
		return "", err
	}

	sum := sha256.Sum256(img.Data)
	name := hex.EncodeToString(sum[:])[:32] + img.Ext
	if err := os.MkdirAll(d.Cfg.ImagesDir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(d.Cfg.ImagesDir, name)
	// Identical content means an identical name; skip rewriting the file.
	if _, statErr := os.Stat(path); statErr != nil {
		if err := writeFileAtomic(path, img.Data); err != nil {
			return "", err
		}
	}
	if header != nil {
		slog.Info("image uploaded", "name", name, "original", header.Filename,
			"bytes", len(img.Data), "width", img.Width, "height", img.Height)
	}
	return "/images/" + name, nil
}
