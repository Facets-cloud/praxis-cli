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
	"strings"
	"time"
)

// sensitiveHeaders are stripped when a redirect leaves the original domain.
// Authorization is listed even though Go strips it itself — the CheckRedirect
// hook below replaces the whole header set, so it has to reapply the rule.
var sensitiveHeaders = []string{"Authorization", "X-Facets-Username"}

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
	req.Method = orig.Method
	req.Header = orig.Header.Clone()
	if !isDomainOrSubdomain(req.URL.Hostname(), orig.URL.Hostname()) {
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

// isDomainOrSubdomain mirrors Go's own sensitive-header rule: the target is the
// original host or a subdomain of it (so apex → www stays covered).
func isDomainOrSubdomain(child, parent string) bool {
	child, parent = strings.ToLower(child), strings.ToLower(parent)
	if child == parent {
		return true
	}
	return strings.HasSuffix(child, "."+parent)
}
