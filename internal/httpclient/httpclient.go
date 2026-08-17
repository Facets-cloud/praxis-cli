// Package httpclient builds the one http.Client every authenticated call to a
// Praxis deployment goes through.
//
// It exists because of a redirect footgun. Go's own transport drops
// Authorization (and Cookie) when a redirect leaves the original domain, but it
// knows nothing about X-Facets-Username, the header that carries a facets-mode
// caller's control-plane identity. A deployment that 30x-redirects to another
// host would therefore forward that email to it. Every call site that sends
// credentials.Profile.Auth() headers must use this client so the rule is
// enforced in one place rather than remembered ten times.
package httpclient

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// sensitiveHeaders must not survive a redirect that leaves the original domain
// or downgrades the scheme. Go already strips its own list on a domain change,
// but not on an https→http downgrade (that isn't a domain change) — and it has
// never heard of X-Facets-Username.
var sensitiveHeaders = []string{"Authorization", "Cookie", "Cookie2", "X-Facets-Username"}

// New returns a client that carries a request's method, body, and auth headers
// across redirects, dropping the auth headers when the target leaves the
// original domain.
//
// The method/body preservation is not incidental: Go's default policy
// downgrades POST→GET and drops the body on 301/302/303, so a gateway that
// redirects to its canonical host would turn every MCP invoke into a body-less
// GET that 404s.
func New(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, CheckRedirect: checkRedirect}
}

func checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return errors.New("stopped after 10 redirects")
	}
	orig := via[0]
	// Restore the method and body only. req.Header is the set net/http already
	// prepared, with ITS sensitive headers stripped if the domain changed —
	// cloning orig.Header over it would undo that and hand a foreign host the
	// cookies Go had just removed.
	req.Method = orig.Method
	if !isDomainOrSubdomain(req.URL.Hostname(), orig.URL.Hostname()) || isDowngrade(orig.URL, req.URL) {
		for _, h := range sensitiveHeaders {
			req.Header.Del(h)
		}
	}
	if orig.GetBody != nil {
		b, err := orig.GetBody()
		if err != nil {
			return err
		}
		req.Body = b
		req.ContentLength = orig.ContentLength
	}
	return nil
}

// isDowngrade reports an https→http redirect. Go doesn't treat this as
// sensitive because the host may be unchanged, but it would put a bearer token
// on the wire in cleartext.
func isDowngrade(from, to *url.URL) bool {
	return from.Scheme == "https" && to.Scheme != "https"
}

// isDomainOrSubdomain mirrors Go's own sensitive-header rule: the target is the
// original host or a subdomain of it (so apex → www stays covered).
func isDomainOrSubdomain(child, parent string) bool {
	child, parent = strings.ToLower(child), strings.ToLower(parent)
	if child == parent {
		return true
	}
	return strings.HasSuffix(child, "."+parent)
}
