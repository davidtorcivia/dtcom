package pgp

import "testing"

func TestWKDHashRFCExample(t *testing.T) {
	// draft-koch-openpgp-webkey-service: Joe.Doe@Example.ORG
	got := WKDHash("Joe.Doe")
	want := "iy9q119eutrkn8s1mk4r39qejnbu3n5q"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	if !IsZBase32(got) {
		t.Fatalf("%q is not a WKD filename", got)
	}
}

func TestWKDHashDavid(t *testing.T) {
	got := WKDHash("david")
	want := "ij4dwniq3d57xexontbfyqkz6um4fitw"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestPublishWKD(t *testing.T) {
	cases := []struct {
		email, base string
		want        bool
	}{
		{"david@davidtorcivia.com", "https://davidtorcivia.com", true},
		{"david@davidtorcivia.com", "https://davidtorcivia.com/", true},
		{"David@DavidTorcivia.com", "https://DAVIDTORCIVIA.COM", true},
		{"david@gmail.com", "https://davidtorcivia.com", false},
		{"david@davidtorcivia.com", "https://www.davidtorcivia.com", false},
		{"", "https://davidtorcivia.com", false},
		{"david@davidtorcivia.com", "", false},
		{"not-an-email", "https://davidtorcivia.com", false},
	}
	for _, tc := range cases {
		if got := PublishWKD(tc.email, tc.base); got != tc.want {
			t.Errorf("PublishWKD(%q, %q) = %v, want %v", tc.email, tc.base, got, tc.want)
		}
	}
}

func TestValidWKDDomain(t *testing.T) {
	if !ValidWKDDomain("davidtorcivia.com") {
		t.Fatal("apex should be valid")
	}
	for _, bad := range []string{"", "nodot", "../etc", "foo/bar", "A.COM", "-x.com", "x.com-"} {
		if ValidWKDDomain(bad) {
			t.Errorf("%q should be rejected", bad)
		}
	}
}

func TestIsZBase32(t *testing.T) {
	if IsZBase32("short") {
		t.Fatal("accepted a short string")
	}
	if IsZBase32("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA") {
		t.Fatal("accepted a letter outside the alphabet")
	}
}
