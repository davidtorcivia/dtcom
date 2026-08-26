package pgp

import (
	"encoding/base64"
	"errors"
	"strings"
)

const (
	beginPublic = "-----BEGIN PGP PUBLIC KEY BLOCK-----"
	endPublic   = "-----END PGP PUBLIC KEY BLOCK-----"
	beginPriv   = "-----BEGIN PGP PRIVATE KEY BLOCK-----"
)

// DecodeArmor returns the binary OpenPGP packets from an ASCII-armored public
// key. A private-key block is refused: that would be published at /pgp.asc.
func DecodeArmor(s string) ([]byte, error) {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	if strings.Contains(s, beginPriv) {
		return nil, errors.New("refusing a private key")
	}
	start := strings.Index(s, beginPublic)
	stop := strings.Index(s, endPublic)
	if start < 0 || stop < 0 || stop <= start {
		return nil, errors.New("not an ASCII-armored public key")
	}
	body := s[start+len(beginPublic) : stop]
	// Armor headers (Comment, Version, …) run until the first blank line.
	if i := strings.Index(body, "\n\n"); i >= 0 {
		body = body[i+2:]
	} else {
		body = strings.TrimLeft(body, "\n")
	}
	var b64 strings.Builder
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "=") {
			continue
		}
		b64.WriteString(line)
	}
	out, err := base64.StdEncoding.DecodeString(b64.String())
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, errors.New("armored public key decoded to nothing")
	}
	return out, nil
}

// FingerprintFromArmor reads a 40-hex-digit fingerprint out of a Comment
// header, the form keys.openpgp.org puts on a VKS response. Empty when the
// block has no such header: we do not parse OpenPGP packets just to display
// one.
func FingerprintFromArmor(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		rest, ok := strings.CutPrefix(line, "Comment:")
		if !ok {
			continue
		}
		hex := compactHex(rest)
		if len(hex) == 40 {
			return hex
		}
	}
	return ""
}

// FormatFingerprint groups a fingerprint into the usual 4-character blocks.
// Empty when s is not hex.
func FormatFingerprint(s string) string {
	hex := compactHex(s)
	if hex == "" {
		return ""
	}
	var b strings.Builder
	for i := 0; i < len(hex); i += 4 {
		if i > 0 {
			b.WriteByte(' ')
		}
		end := i + 4
		if end > len(hex) {
			end = len(hex)
		}
		b.WriteString(hex[i:end])
	}
	return b.String()
}

func compactHex(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == ' ' || r == '\t':
			continue
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r >= 'a' && r <= 'f':
			b.WriteRune(r - ('a' - 'A'))
		case r >= 'A' && r <= 'F':
			b.WriteRune(r)
		default:
			return ""
		}
	}
	return b.String()
}
