package build

import (
	"log/slog"
	"path/filepath"
	"strings"

	"davidtorcivia.com/dtcom/internal/pgp"
	"davidtorcivia.com/dtcom/internal/siteconfig"
)

// ContactKey is what the public templates get when a public key was resolved
// for the site's contact address.
type ContactKey struct {
	Email              string
	Mailto             string
	Fingerprint        string
	FingerprintCompact string
	Armored            string
}

type pgpMaterial struct {
	page   ContactKey
	binary []byte
	wkd    bool
	hash   string
	domain string
}

func (e *Engine) pageVars(site *siteconfig.Config, extra map[string]any) map[string]any {
	out := map[string]any{"Site": site}
	if e.pgpOut != nil {
		out["PGP"] = e.pgpOut.page
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

// PGPEnabled reports whether a key cache is configured.
func (e *Engine) PGPEnabled() bool {
	return e != nil && e.cfg.PGP != nil
}

// PGPRecord is the on-disk cache, for the admin status line. It does not
// talk to the keyserver.
func (e *Engine) PGPRecord() *pgp.Record {
	if !e.PGPEnabled() {
		return nil
	}
	rec, err := e.cfg.PGP.Load()
	if err != nil {
		return nil
	}
	return rec
}

// RefreshPGP forces a keyserver lookup for the current contact address, then
// rebuilds. A lookup error is returned after the rebuild so the last good key
// (if any) stays published.
func (e *Engine) RefreshPGP() error {
	if !e.PGPEnabled() {
		return nil
	}
	var email string
	if s := e.cfg.Site(); s != nil {
		email = s.ContactEmail()
	}
	_, lookupErr := e.cfg.PGP.Resolve(email, true)
	if err := e.Rebuild(); err != nil && lookupErr == nil {
		return err
	}
	return lookupErr
}

func (e *Engine) resolvePGP() {
	e.pgpOut = nil
	if e.cfg.PGP == nil {
		return
	}
	site := e.cfg.Site()
	if site == nil {
		return
	}
	email := site.ContactEmail()
	rec, err := e.cfg.PGP.Resolve(email, false)
	if err != nil {
		slog.Warn("pgp lookup", "email", email, "err", err)
	}
	if rec == nil || !rec.Found() {
		return
	}
	binary, err := pgp.DecodeArmor(rec.Armored)
	if err != nil {
		slog.Warn("pgp armor", "err", err)
		return
	}
	fp := rec.Fingerprint
	if fp == "" {
		fp = pgp.FingerprintFromArmor(rec.Armored)
	}
	out := &pgpMaterial{
		page: ContactKey{
			Email:              rec.Email,
			Mailto:             site.ContactHref(),
			Fingerprint:        pgp.FormatFingerprint(fp),
			FingerprintCompact: fp,
			Armored:            strings.TrimSpace(rec.Armored),
		},
		binary: binary,
	}
	if pgp.PublishWKD(rec.Email, site.BaseURL) {
		local := pgp.WKDLocalPart(rec.Email)
		out.wkd = local != ""
		out.hash = pgp.WKDHash(local)
		out.domain = pgp.EmailDomain(rec.Email)
	}
	e.pgpOut = out
}

func (e *Engine) renderPGP(written *pathSet) error {
	if e.pgpOut == nil {
		return nil
	}
	if err := e.writeFile(filepath.Join(e.outputRoot(), "pgp.asc"), []byte(e.pgpOut.page.Armored+"\n"), written); err != nil {
		return err
	}
	if !e.pgpOut.wkd {
		return nil
	}
	root := filepath.Join(e.outputRoot(), ".well-known", "openpgpkey")
	policy := []byte("\n")
	if err := e.writeFile(filepath.Join(root, "policy"), policy, written); err != nil {
		return err
	}
	if err := e.writeFile(filepath.Join(root, "hu", e.pgpOut.hash), e.pgpOut.binary, written); err != nil {
		return err
	}
	adv := filepath.Join(root, e.pgpOut.domain)
	if err := e.writeFile(filepath.Join(adv, "policy"), policy, written); err != nil {
		return err
	}
	return e.writeFile(filepath.Join(adv, "hu", e.pgpOut.hash), e.pgpOut.binary, written)
}
