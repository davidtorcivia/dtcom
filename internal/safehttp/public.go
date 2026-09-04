// Package safehttp provides HTTP transports that cannot reach local networks.
package safehttp

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"syscall"
	"time"
)

var ErrPrivateAddress = errors.New("refusing to fetch from a private or loopback address")

// PublicIP reports whether ip is routable on the public internet.
func PublicIP(ip net.IP) bool {
	if ip == nil || ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() {
		return false
	}
	v4 := ip.To4()
	return v4 == nil || v4[0] != 100 || v4[1] < 64 || v4[1] > 127
}

// PublicTransport validates the resolved peer immediately before connect.
func PublicTransport(timeout time.Duration) *http.Transport {
	return &http.Transport{
		Proxy: nil,
		DialContext: (&net.Dialer{
			Timeout: timeout,
			Control: func(_, address string, _ syscall.RawConn) error {
				host, _, err := net.SplitHostPort(address)
				if err != nil {
					return err
				}
				if !PublicIP(net.ParseIP(host)) {
					return fmt.Errorf("%w: %s", ErrPrivateAddress, host)
				}
				return nil
			},
		}).DialContext,
	}
}
