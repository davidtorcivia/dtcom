package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"

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

	raw, err := io.ReadAll(file)
	if err != nil {
		return "", err
	}
	if len(raw) == 0 {
		return "", errNoImageField
	}

	var (
		data   []byte
		ext    string
		width  int
		height int
	)
	if isSVG(raw) {
		// SVG is a drawing the decoder cannot open — it is a document, not a
		// raster. It is stored as written, having been checked by parsing
		// rather than by trusting the extension or sniffing a prefix, so a file
		// that merely looks like SVG cannot be served back as image/svg+xml.
		//
		// Being a document is also why it is served under a tightened policy;
		// see svgPolicy in public.go. There is no resampling to do: the whole
		// point of the format is that it has no fixed size.
		data, ext = raw, ".svg"
	} else {
		img, procErr := build.ProcessImage(bytes.NewReader(raw), build.MaxImageDim)
		if procErr != nil {
			return "", procErr
		}
		data, ext, width, height = img.Data, img.Ext, img.Width, img.Height
	}

	sum := sha256.Sum256(data)
	name := hex.EncodeToString(sum[:])[:32] + ext
	if err := os.MkdirAll(d.Cfg.ImagesDir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(d.Cfg.ImagesDir, name)
	// Identical content means an identical name; skip rewriting the file.
	if _, statErr := os.Stat(path); statErr != nil {
		if err := writeFileAtomic(path, data); err != nil {
			return "", err
		}
	}
	if header != nil {
		slog.Info("image uploaded", "name", name, "original", header.Filename,
			"bytes", len(data), "width", width, "height", height)
	}
	if !isSVG(raw) {
		d.generateRenditions(name)
	}
	return "/images/" + name, nil
}

// renditionWork serializes rendition encoding across uploads. Encoding one
// image at every width is several seconds of CPU; two at once on a small box
// would make the site itself slow to answer, and there is nothing to gain from
// racing them.
var renditionWork sync.Mutex

// generateRenditions cuts an uploaded image down to the responsive widths, in
// the background.
//
// Off the request on purpose. Doing it inline tripled the time an upload took
// — ten seconds for a large picture — and that is time the author spends
// watching a spinner in the editor for work that changes nothing about the
// answer they are waiting for: the master is already stored and already serves.
//
// The rebuild at the end is what puts the renditions into the pages. Until it
// runs, a post referencing this image simply points at the master, which is
// correct, just heavier — and if the process dies first, the next startup's
// backfill finishes the job.
func (d *Deps) generateRenditions(name string) {
	go func() {
		renditionWork.Lock()
		defer renditionWork.Unlock()

		written, err := build.GenerateVariants(d.Cfg.ImagesDir, name)
		if err != nil {
			slog.Warn("image renditions", "name", name, "err", err)
		}
		if len(written) == 0 {
			return
		}
		slog.Info("image renditions generated", "name", name, "files", len(written))
		if d.Engine == nil {
			return
		}
		d.Engine.Images().Refresh()
		if err := d.Engine.Rebuild(); err != nil {
			slog.Warn("rebuild after renditions", "name", name, "err", err)
		}
	}()
}
