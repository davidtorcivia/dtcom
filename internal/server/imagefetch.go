package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"syscall"
	"time"
)

// Fetching an image the caller only named by URL.
//
// This is the one place the server makes an outbound request on someone else's
// say-so, which is the shape of a server-side request forgery: the caller picks
// the address, and the request leaves from inside the network rather than from
// wherever they are. Being behind a bearer token narrows who can ask, not what
// the answer reaches — this process can see a loopback admin port, a database,
// and a cloud metadata endpoint that a browser on the far side of the tunnel
// cannot. So the destination is checked rather than trusted.

const (
	// imageFetchTimeout bounds the whole fetch. A picture that has not arrived
	// in half a minute is not going to.
	imageFetchTimeout = 30 * time.Second
	maxImageRedirects = 3
)

var errPrivateAddress = errors.New("refusing to fetch from a private or loopback address")

// publicIP reports whether an address is somewhere on the internet, as opposed
// to somewhere on this machine or this network.
func publicIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() {
		return false
	}
	// 100.64.0.0/10, carrier-grade NAT — also what a Tailscale or similar
	// overlay hands out, so it addresses machines this one can reach and the
	// caller should not. net.IP.IsPrivate does not cover it (it is not one of
	// the RFC 1918 blocks) but it is emphatically not the public internet.
	if v4 := ip.To4(); v4 != nil && v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127 {
		return false
	}
	return true
}

// imageFetchClient refuses to connect anywhere but the public internet.
//
// The check hangs off Dialer.Control rather than off the hostname, because it
// runs after DNS has resolved and before the socket connects, against the
// address actually being dialled. Checking the name instead would be checking a
// different thing than the one the connection uses: a hostname that resolves
// public on the first lookup and loopback on the second (DNS rebinding) passes
// a name check and still lands on 127.0.0.1. Redirects are followed by the same
// client, so each hop is dialled through the same gate.
var imageFetchClient = &http.Client{
	Timeout: imageFetchTimeout,
	CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) >= maxImageRedirects {
			return fmt.Errorf("stopped after %d redirects", maxImageRedirects)
		}
		return nil
	},
	Transport: &http.Transport{
		DialContext: (&net.Dialer{
			Timeout: 10 * time.Second,
			Control: func(network, address string, _ syscall.RawConn) error {
				host, _, err := net.SplitHostPort(address)
				if err != nil {
					return err
				}
				if !publicIP(net.ParseIP(host)) {
					return fmt.Errorf("%w: %s", errPrivateAddress, host)
				}
				return nil
			},
		}).DialContext,
	},
}

// fetchRemoteImage downloads an image the caller named by URL, refusing
// anything that is not plain http(s) to a public address, and reading at most
// one image's worth of it.
func fetchRemoteImage(ctx context.Context, raw string) ([]byte, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("bad url: %w", err)
	}
	// file:// would read the disk and gopher:// and friends are protocol
	// smuggling; an allowlist is the only safe direction here.
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("url must be http or https, got %q", u.Scheme)
	}
	if u.Host == "" {
		return nil, errors.New("url has no host")
	}

	ctx, cancel := context.WithTimeout(ctx, imageFetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "image/*")
	req.Header.Set("User-Agent", "dtcom (+image import)")

	resp, err := imageFetchClient.Do(req)
	if err != nil {
		// The dial gate's error arrives wrapped in url.Error; unwrapping keeps
		// the caller's message about the actual refusal rather than about
		// transport plumbing.
		if errors.Is(err, errPrivateAddress) {
			return nil, errPrivateAddress
		}
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch %s: %s", u.Redacted(), resp.Status)
	}

	// One byte over the cap rather than exactly the cap, so a file that is
	// merely too large is reported as too large instead of being silently
	// truncated into a corrupt image.
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxImageUpload+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxImageUpload {
		return nil, errImageTooLarge
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("fetch %s: empty response", u.Redacted())
	}
	return data, nil
}
