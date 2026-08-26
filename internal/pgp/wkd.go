package pgp

import (
	"crypto/sha1"
	"net/url"
	"strings"
)

// zbase32Alphabet is RFC 6189 §5.1.6, which Web Key Directory uses to encode
// the SHA-1 of an email local-part as a filename.
const zbase32Alphabet = "ybndrfg8ejkmcpqxot1uwisza345h769"

// WKDHash is the 32-character z-base-32 SHA-1 of a lowercased local-part, used
// as the `hu/<hash>` filename in both WKD layouts.
func WKDHash(localPart string) string {
	sum := sha1.Sum([]byte(strings.ToLower(localPart)))
	return zbase32(sum[:])
}

// WKDLocalPart returns the local-part of email, or empty if it is not an
// address.
func WKDLocalPart(email string) string {
	local, _, ok := strings.Cut(email, "@")
	if !ok {
		return ""
	}
	return local
}

// EmailDomain is the lowercased domain of email, or empty.
func EmailDomain(email string) string {
	_, domain, ok := strings.Cut(email, "@")
	if !ok {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(domain))
}

// SiteHost is the lowercased hostname of a base URL such as
// https://davidtorcivia.com. Empty when the URL will not parse.
func SiteHost(baseURL string) string {
	u, err := url.Parse(baseURL)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

// PublishWKD reports whether a key for email should be published under this
// site's WKD paths. WKD is looked up on the email's own domain, so a Gmail
// address on a personal site must not appear at /.well-known/openpgpkey.
func PublishWKD(email, baseURL string) bool {
	domain := EmailDomain(email)
	host := SiteHost(baseURL)
	return domain != "" && host != "" && domain == host
}

func zbase32(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	// Most-significant bit first. SHA-1 is 160 bits, which divides evenly by
	// 5, so this produces 32 characters and needs no padding.
	nbits := len(data) * 8
	out := make([]byte, (nbits+4)/5)
	for i := range out {
		var idx byte
		for b := 0; b < 5; b++ {
			pos := i*5 + b
			if pos >= nbits {
				break
			}
			by := pos / 8
			bi := 7 - (pos % 8)
			if data[by]&(1<<bi) != 0 {
				idx |= 1 << (4 - b)
			}
		}
		out[i] = zbase32Alphabet[idx]
	}
	return string(out)
}

// IsZBase32 reports whether s is a 32-character WKD hash filename.
func IsZBase32(s string) bool {
	if len(s) != 32 {
		return false
	}
	for _, r := range s {
		if !strings.ContainsRune(zbase32Alphabet, r) {
			return false
		}
	}
	return true
}

// ValidWKDDomain is the email-domain path segment in the advanced WKD layout.
func ValidWKDDomain(s string) bool {
	if s == "" || len(s) > 253 || strings.Contains(s, "..") {
		return false
	}
	if s[0] == '.' || s[len(s)-1] == '.' || s[0] == '-' || s[len(s)-1] == '-' {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '-':
		default:
			return false
		}
	}
	return strings.ContainsRune(s, '.')
}
