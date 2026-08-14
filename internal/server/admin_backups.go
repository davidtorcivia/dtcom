package server

// The backups page: take one, download one, put one back.

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"davidtorcivia.com/dtcom/internal/backup"
)

// errBackupsOff is returned when the service was not wired — no images or data
// directory, which is only the case in a stripped-down test harness.
var errBackupsOff = errors.New("backups are not configured")

func registerAdminBackups(mux *http.ServeMux, d *Deps) {
	mux.HandleFunc("GET /admin/backups", d.requireAuth(d.adminBackups))
	mux.HandleFunc("POST /admin/backups", d.requireAuth(d.adminBackupCreate))
	mux.HandleFunc("GET /admin/backups/{name}/download", d.requireAuth(d.adminBackupDownload))
	mux.HandleFunc("POST /admin/backups/{name}/restore", d.requireAuth(d.adminBackupRestore))
	mux.HandleFunc("POST /admin/backups/{name}/delete", d.requireAuth(d.adminBackupDelete))
}

// backupRow is one archive as the template renders it.
type backupRow struct {
	Name     string
	Kind     string
	Created  time.Time
	Size     string
	Ago      string
	Confirm  string // what the operator has to type to restore this one
	IsSafety bool
}

func (d *Deps) adminBackups(w http.ResponseWriter, r *http.Request) {
	d.renderBackups(w, r, "", "")
}

func (d *Deps) renderBackups(w http.ResponseWriter, r *http.Request, notice, errMsg string) {
	if d.Backups == nil || !d.adminReady(w) {
		if d.Backups == nil {
			writeError(w, http.StatusServiceUnavailable, errBackupsOff)
			return
		}
		return
	}
	// A message survives the redirect after an action in the query string,
	// which keeps the POST-redirect-GET shape the rest of the admin uses.
	if notice == "" {
		notice = r.URL.Query().Get("ok")
	}
	if errMsg == "" {
		errMsg = r.URL.Query().Get("err")
	}

	list, err := d.Backups.List()
	if err != nil {
		errMsg = err.Error()
	}
	rows := make([]backupRow, 0, len(list))
	var total int64
	for _, in := range list {
		total += in.Size
		rows = append(rows, backupRow{
			Name:     in.Name,
			Kind:     string(in.Kind),
			Created:  in.Created.Local(),
			Size:     humanBytes(in.Size),
			Ago:      humanAge(in.Age()),
			Confirm:  confirmPhrase(in),
			IsSafety: in.Kind == backup.KindPreRestore,
		})
	}

	policy := d.Backups.Policy()
	pooled, poolBytes := d.Backups.PoolSize()
	d.adminTmpls.render(w, "backups", d.adminData("Backups", map[string]any{
		"Backups":     rows,
		"Total":       humanBytes(total),
		"Destination": d.Backups.Where(),
		"Schedule":    humanInterval(d.Backups.Interval()),
		"Policy": fmt.Sprintf("the last %d, plus the %d most recent taken before a restore",
			policy.Keep, policy.Safety),
		"Pool":   fmt.Sprintf("%d images, %s, shared by every archive", pooled, humanBytes(poolBytes)),
		"Notice": notice,
		"Error":  errMsg,
	}))
}

// confirmPhrase is what the restore form makes the operator type. The date of
// the archive being restored, so that confirming is an act of reading the row
// rather than of clicking through a dialog.
func confirmPhrase(in backup.Info) string {
	return in.Created.Local().Format("2006-01-02")
}

func (d *Deps) adminBackupCreate(w http.ResponseWriter, r *http.Request) {
	if d.Backups == nil {
		writeError(w, http.StatusServiceUnavailable, errBackupsOff)
		return
	}
	info, err := d.Backups.Create(backup.KindManual)
	if errors.Is(err, backup.ErrUnchanged) {
		// Not a failure, and worth saying rather than quietly writing a second
		// copy of a state that is already saved.
		redirectBackups(w, r, fmt.Sprintf(
			"Nothing has changed since %s was taken %s, so no new backup was needed.",
			info.Name, humanAge(info.Age())), "")
		return
	}
	if err != nil {
		slog.Error("backup failed", "err", err)
		redirectBackups(w, r, "", "Backup failed: "+err.Error())
		return
	}
	slog.Info("backup taken", "name", info.Name, "bytes", info.Size, "kind", info.Kind)
	if removed, swept, err := d.Backups.Prune(); err != nil {
		slog.Warn("prune after backup", "err", err)
	} else if len(removed) > 0 || swept > 0 {
		slog.Info("old backups pruned", "archives", len(removed), "pooled_images", swept)
	}
	redirectBackups(w, r, fmt.Sprintf("Backed up: %s (%s)", info.Name, humanBytes(info.Size)), "")
}

func (d *Deps) adminBackupDownload(w http.ResponseWriter, r *http.Request) {
	if d.Backups == nil {
		writeError(w, http.StatusServiceUnavailable, errBackupsOff)
		return
	}
	name := r.PathValue("name")

	// Checked before a single header is written. The response is streamed and
	// assembled as it goes, so by the time anything could go wrong the status
	// code has already been sent — "no such backup" has to be answered here or
	// it comes out as an empty 200.
	if _, err := d.Backups.Stat(name); err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}

	// What is stored holds no images — they are pooled and shared. What is sent
	// is the complete thing, assembled as it streams, because a download is the
	// copy that outlives this disk and it cannot depend on a pool left behind
	// on it. No Content-Length: the size is not known until it has been built,
	// and a wrong one is worse than none.
	w.Header().Set("Content-Type", "application/gzip")
	// The name is validated as a plain archive name before it gets here, so it
	// carries no quote or newline to break out of the header with.
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	// A backup is the site's private data; no cache anywhere should hold it.
	w.Header().Set("Cache-Control", "no-store, private")
	if err := d.Backups.Download(name, w); err != nil {
		// Too late for a status code if bytes are already out; the log is the
		// only place left to say so, and the truncated file will not restore.
		slog.Warn("backup download failed", "name", name, "err", err)
	}
}

func (d *Deps) adminBackupRestore(w http.ResponseWriter, r *http.Request) {
	if d.Backups == nil {
		writeError(w, http.StatusServiceUnavailable, errBackupsOff)
		return
	}
	name := r.PathValue("name")

	// Bounded and parsed the same way every other admin form is, rather than
	// leaving it to FormValue to read an unbounded body on its own.
	if !parseAdminForm(w, r) {
		return
	}

	// The typed confirmation is checked here rather than in the browser. A
	// dialog can be dismissed by a script or a stray Enter; this cannot.
	list, err := d.Backups.List()
	if err != nil {
		redirectBackups(w, r, "", err.Error())
		return
	}
	var target *backup.Info
	for i := range list {
		if list[i].Name == name {
			target = &list[i]
			break
		}
	}
	if target == nil {
		redirectBackups(w, r, "", "No such backup: "+name)
		return
	}
	if strings.TrimSpace(r.FormValue("confirm")) != confirmPhrase(*target) {
		redirectBackups(w, r, "", "Restore cancelled: the confirmation did not match "+confirmPhrase(*target)+".")
		return
	}

	slog.Warn("restore starting", "name", name)
	res, err := d.Backups.Restore(name)
	if err != nil {
		slog.Error("restore failed", "name", name, "err", err)
		msg := "Restore failed: " + err.Error()
		if res != nil && res.Safety.Name != "" {
			msg += " The site as it was before this attempt is in " + res.Safety.Name + "."
		}
		redirectBackups(w, r, "", msg)
		return
	}

	// The pages are rebuilt before answering, so that by the time the operator
	// reads "restored" the site is actually serving the restored posts. That
	// part is fast — it is rendering markdown.
	//
	// The renditions are not fast, and they are not needed for the site to be
	// correct: a picture whose renditions are missing is referenced at full
	// size, which is heavier and right. So they are made afterwards, on the
	// same path startup uses, and the rebuild that follows adds the srcsets
	// back. Doing it inline instead put the whole encode inside the request —
	// seconds for one image, and a timeout away from failing on a large
	// library after having already succeeded.
	if err := d.Engine.Rebuild(); err != nil {
		slog.Error("rebuild after restore", "err", err)
		redirectBackups(w, r, "",
			"Restored, but the site failed to rebuild: "+err.Error()+" — the files are in place; try Regenerate.")
		return
	}

	slog.Warn("restore complete", "name", name, "files", res.Files, "removed", res.Removed,
		"safety", res.Safety.Name)
	d.regenerateRenditions()
	redirectBackups(w, r, fmt.Sprintf(
		"Restored %s — %d files in place, %d removed. The state from just before this is saved as %s.",
		name, res.Files, res.Removed, res.Safety.Name), "")
}

func (d *Deps) adminBackupDelete(w http.ResponseWriter, r *http.Request) {
	if d.Backups == nil {
		writeError(w, http.StatusServiceUnavailable, errBackupsOff)
		return
	}
	name := r.PathValue("name")
	if err := d.Backups.Delete(name); err != nil {
		redirectBackups(w, r, "", err.Error())
		return
	}
	slog.Info("backup deleted", "name", name)
	redirectBackups(w, r, "Deleted "+name+".", "")
}

// regenerateRenditions makes the responsive sizes for whatever a restore
// brought back, then rebuilds so the pages start referencing them.
//
// Shares the lock the upload path uses, so a restore landing while an upload is
// still encoding waits its turn rather than competing for the machine.
func (d *Deps) regenerateRenditions() {
	ix := d.Engine.Images()
	if ix == nil {
		return
	}
	d.renditionWG.Add(1)
	go func() {
		defer d.renditionWG.Done()
		renditionWork.Lock()
		defer renditionWork.Unlock()

		n, err := ix.Backfill()
		if err != nil {
			slog.Warn("renditions after restore", "err", err)
		}
		if n == 0 {
			return
		}
		slog.Info("renditions regenerated after restore", "files", n)
		if err := d.Engine.Rebuild(); err != nil {
			slog.Warn("rebuild after restore renditions", "err", err)
		}
	}()
}

func redirectBackups(w http.ResponseWriter, r *http.Request, notice, errMsg string) {
	q := url.Values{}
	if notice != "" {
		q.Set("ok", notice)
	}
	if errMsg != "" {
		q.Set("err", errMsg)
	}
	target := "/admin/backups"
	if len(q) > 0 {
		target += "?" + q.Encode()
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGT"[exp])
}

func humanAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d hr ago", int(d.Hours()))
	case d < 48*time.Hour:
		return "yesterday"
	default:
		return fmt.Sprintf("%d days ago", int(d.Hours()/24))
	}
}

func humanInterval(d time.Duration) string {
	switch {
	case d == 0:
		return "off — backups are only taken when you ask"
	case d == 24*time.Hour:
		return "daily"
	case d == time.Hour:
		return "hourly"
	case d < time.Hour:
		return fmt.Sprintf("every %d minutes", int(d.Minutes()))
	case d%(24*time.Hour) == 0:
		return fmt.Sprintf("every %d days", int(d.Hours()/24))
	default:
		return fmt.Sprintf("every %d hours", int(d.Hours()))
	}
}
