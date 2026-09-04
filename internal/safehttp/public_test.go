package safehttp

import (
	"net"
	"testing"
)

func TestPublicIP(t *testing.T) {
	for _, tc := range []struct {
		ip   string
		want bool
	}{
		{"127.0.0.1", false}, {"10.0.0.1", false}, {"169.254.169.254", false},
		{"100.64.0.1", false}, {"100.127.255.254", false}, {"8.8.8.8", true},
		{"::1", false}, {"fe80::1", false}, {"2606:4700:4700::1111", true},
	} {
		if got := PublicIP(net.ParseIP(tc.ip)); got != tc.want {
			t.Errorf("PublicIP(%s) = %v, want %v", tc.ip, got, tc.want)
		}
	}
}
