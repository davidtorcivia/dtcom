package pgp

import (
	"bytes"
	"testing"
)

const testArmored = `-----BEGIN PGP PUBLIC KEY BLOCK-----
Comment: 0956 E8AA E515 2D94 50B1  CA3F 4B88 B196 0413 50BC
Comment: Test <a@b.c>

d2tkLWJpbmFyeS1ib2R5
=xxxx
-----END PGP PUBLIC KEY BLOCK-----
`

func TestDecodeArmor(t *testing.T) {
	got, err := DecodeArmor(testArmored)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "wkd-binary-body" {
		t.Fatalf("got %q", got)
	}
}

func TestDecodeArmorCRLF(t *testing.T) {
	crlf := stringsReplaceCRLF(testArmored)
	got, err := DecodeArmor(crlf)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "wkd-binary-body" {
		t.Fatalf("got %q", got)
	}
}

func stringsReplaceCRLF(s string) string {
	var b bytes.Buffer
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			b.WriteString("\r\n")
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func TestDecodeArmorRejectsPrivate(t *testing.T) {
	priv := "-----BEGIN PGP PRIVATE KEY BLOCK-----\n\nd2tkLWJpbmFyeS1ib2R5\n-----END PGP PRIVATE KEY BLOCK-----\n"
	if _, err := DecodeArmor(priv); err == nil {
		t.Fatal("accepted a private key")
	}
}

func TestDecodeArmorRejectsGarbage(t *testing.T) {
	if _, err := DecodeArmor("not a key"); err == nil {
		t.Fatal("accepted garbage")
	}
}

func TestFingerprintFromArmor(t *testing.T) {
	got := FingerprintFromArmor(testArmored)
	want := "0956E8AAE5152D9450B1CA3F4B88B196041350BC"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	if FingerprintFromArmor("-----BEGIN PGP PUBLIC KEY BLOCK-----\n\nd2tkLWJpbmFyeS1ib2R5\n-----END PGP PUBLIC KEY BLOCK-----") != "" {
		t.Fatal("invented a fingerprint from a block with no Comment")
	}
	// A name Comment must not be scraped for incidental hex letters.
	named := "-----BEGIN PGP PUBLIC KEY BLOCK-----\nComment: David Torcivia <a@b.c>\n\nd2tkLWJpbmFyeS1ib2R5\n-----END PGP PUBLIC KEY BLOCK-----\n"
	if FingerprintFromArmor(named) != "" {
		t.Fatal("took a name Comment as a fingerprint")
	}
}

func TestFormatFingerprint(t *testing.T) {
	got := FormatFingerprint("0956e8aae5152d9450b1ca3f4b88b196041350bc")
	want := "0956 E8AA E515 2D94 50B1 CA3F 4B88 B196 0413 50BC"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	if FormatFingerprint("") != "" {
		t.Fatal("empty in should stay empty")
	}
}
