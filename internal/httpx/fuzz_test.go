package httpx

import (
	"context"
	"net"
	"net/url"
	"strings"
	"testing"
)

// FuzzBase: a base address is either refused with an error, or it is an http
// or https URL with a host and nothing else that could redirect a request: no
// userinfo, query, fragment, whitespace or dot segment.
func FuzzBase(f *testing.F) {
	for _, s := range []string{
		"http://example.test", "https://example.test:8443/p", "http://[::1]:9090", "http://192.0.2.7:9090/a/b",
		"", "localhost:9090", "ftp://example.test", "http://user:pw@example.test", "http://example.test?x=1",
		"http://example.test#f", "http://example.test/a/../b", "http://example.test/./a", "http://exa mple.test",
		"//example.test", "http:example.test", "http://example.test\\@evil.test", "http://example.test/%2e%2e/x",
		"http://example.test/a%2fb", "HTTP://EXAMPLE.TEST", "http://\x00example.test",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		c, err := New(Config{Base: s})
		if err != nil {
			if Code(err) == "" {
				t.Fatalf("untyped error: %v", err)
			}
			return
		}
		u := c.base
		if u.Scheme != "http" && u.Scheme != "https" {
			t.Fatalf("scheme %q accepted from %q", u.Scheme, s)
		}
		if u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
			t.Fatalf("unsafe base accepted from %q: %+v", s, u)
		}
		if hasDotSegment(u.Path) || strings.ContainsAny(s, " \t\r\n\\") {
			t.Fatalf("base %q accepted with a dot segment or whitespace", s)
		}
	})
}

// FuzzPath: an accepted path is a plain absolute path that survives a parse
// unchanged, and a refused path never reaches the dialer.
func FuzzPath(f *testing.F) {
	for _, s := range []string{
		"/api/v1/targets", "/-/ready", "/api/health", "", "/", "api/v1", "//evil.test/x", "/a/../b", "/a/./b",
		"/a?x=1", "/a#f", "/a b", "/a%2e%2e/b", "/a\\b", "/a\nb", "http://example.test/x", "/a//b", "/%2e%2e", "/é",
		// One reserved character each, so a rule that lets any of them
		// through is caught by the seed run and not only by luck.
		"/a?b", "/a#b", "/a%2e", "/a;b", "/a:b", "/a@b", "/a*b", "/a+b", "/a=b", "/a&b", "/a,b", "/a!b", "/a~b", "/a\"b",
	} {
		f.Add(s)
	}
	var dialed bool
	c, err := newClient(Config{Base: "http://example.test"}, func(context.Context, string, string) (net.Conn, error) {
		dialed = true
		return nil, &net.OpError{Op: "dial", Err: net.ErrClosed}
	})
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, p string) {
		dialed = false
		_, err := c.Get(context.Background(), Request{Path: p})
		if err == nil {
			t.Fatal("the dialer always fails, so Get cannot succeed")
		}
		if !validPath(p) {
			if dialed {
				t.Fatalf("a request went out for the refused path %q", p)
			}
			return
		}
		u, perr := url.Parse("http://example.test" + p)
		if perr != nil || u.Path != p || u.RawQuery != "" || u.Fragment != "" || u.Host != "example.test" {
			t.Fatalf("accepted path %q does not survive a parse: %+v (%v)", p, u, perr)
		}
		if strings.Contains(p, "//") || hasDotSegment(p) || p[0] != '/' {
			t.Fatalf("accepted path %q has a doubled slash, a dot segment or no root", p)
		}
		for i := 0; i < len(p); i++ {
			if c := p[i]; !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '/' || c == '-' || c == '_' || c == '.') {
				t.Fatalf("accepted path %q holds %q", p, c)
			}
		}
	})
}
