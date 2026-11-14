// Package httpx is the only place in Vigil that talks HTTP. Every backend
// client (Prometheus, Grafana) goes through one Client per backend, and the
// rules are fixed here so no caller can loosen them:
//
//   - GET only: there is no method parameter and no request body.
//   - The URL is the overlay's base URL plus a fixed path plus encoded
//     parameters. Callers cannot hand in a URL.
//   - A redirect is never followed (redirect_refused).
//   - The transport has no proxy, so HTTP_PROXY and friends are ignored.
//   - Every call has a deadline and a body cap, and at most two calls are in
//     flight per Client.
//   - Errors carry a stable detail code and nothing else: no URL, no address,
//     no text from the backend.
//
// depguard denies net/http everywhere else outside tests.
package httpx

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Hedronite/vigil/internal/reasons"
)

// Limits.
const (
	// PromMaxBody is the body cap for Prometheus calls.
	PromMaxBody int64 = 8 << 20
	// GrafanaMaxBody is the body cap for Grafana calls.
	GrafanaMaxBody int64 = 16 << 10
	// HardMaxBody is the ceiling no call can raise its cap above.
	HardMaxBody = PromMaxBody
	// HardMaxTimeout is the ceiling no call can raise its deadline above.
	HardMaxTimeout = 10 * time.Second
	// DefaultTimeout applies when neither the Client nor the call sets one.
	DefaultTimeout = 5 * time.Second
	// MaxInFlight is the number of concurrent calls per Client.
	MaxInFlight = 2

	maxHeaderBytes = 64 << 10
)

// Config builds a Client.
type Config struct {
	// Base is the backend's base URL from the overlay: http or https, a host,
	// optionally a port and a path prefix. No credentials, query or fragment.
	Base string
	// MaxBody is the default body cap for calls, at most HardMaxBody.
	MaxBody int64
	// Timeout is the default deadline for calls, at most HardMaxTimeout.
	Timeout time.Duration
}

// Request is one GET.
type Request struct {
	// Path is fixed by the calling client, for example "/api/v1/targets".
	Path string
	// Query is encoded with url.Values.Encode.
	Query url.Values
	// Timeout overrides the Client default; zero keeps it.
	Timeout time.Duration
	// MaxBody overrides the Client default cap; zero keeps it.
	MaxBody int64
}

// Response is a completed call with a body inside its cap. Any status other
// than a redirect comes back here; Err turns a non-2xx status into an error.
type Response struct {
	Status      int
	ContentType string
	Body        []byte
}

// Err reports http_status for any non-2xx answer.
func (r *Response) Err() error {
	if r.Status >= 200 && r.Status < 300 {
		return nil
	}
	return &Error{Code: reasons.HTTPStatus, Status: r.Status}
}

// Error is a failed call. It prints the detail code and nothing else, so it
// is safe to log: the underlying error would carry the URL.
type Error struct {
	// Code is one of the reasons detail codes: unreachable, timeout,
	// body_cap, redirect_refused or http_status.
	Code string
	// Status is the HTTP status for http_status, otherwise zero.
	Status int
}

// Error implements error.
func (e *Error) Error() string { return e.Code }

// Code returns the detail code of a httpx error, or "" for any other error.
func Code(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

var errRedirect = errors.New("redirect refused")

// Client is the bounded client for one backend.
type Client struct {
	base    url.URL
	maxBody int64
	timeout time.Duration
	hc      *http.Client
	sem     chan struct{}
}

// New validates cfg and builds the Client.
func New(cfg Config) (*Client, error) { return newClient(cfg, nil) }

type dialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

// newClient is New with a test hook for the dialer. A nil dial uses the
// system dialer.
func newClient(cfg Config, dial dialFunc) (*Client, error) {
	base, err := parseBase(cfg.Base)
	if err != nil {
		return nil, err
	}
	c := &Client{
		base:    *base,
		maxBody: capBody(cfg.MaxBody, HardMaxBody),
		timeout: capTimeout(cfg.Timeout, DefaultTimeout),
		sem:     make(chan struct{}, MaxInFlight),
	}
	d := &net.Dialer{Timeout: HardMaxTimeout}
	dc := d.DialContext
	if dial != nil {
		dc = dial
	}
	tr := &http.Transport{
		// Proxy stays nil on purpose: HTTP_PROXY, HTTPS_PROXY and NO_PROXY
		// are ignored.
		Proxy:                  nil,
		DialContext:            dc,
		TLSHandshakeTimeout:    HardMaxTimeout,
		ResponseHeaderTimeout:  HardMaxTimeout,
		MaxResponseHeaderBytes: maxHeaderBytes,
		DisableKeepAlives:      true,
		ForceAttemptHTTP2:      false,
	}
	c.hc = &http.Client{
		Transport: tr,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errRedirect
		},
	}
	return c, nil
}

func capBody(v, ceiling int64) int64 {
	if v <= 0 || v > ceiling {
		return ceiling
	}
	return v
}

func capTimeout(v, def time.Duration) time.Duration {
	if v <= 0 {
		v = def
	}
	if v > HardMaxTimeout {
		v = HardMaxTimeout
	}
	return v
}

// parseBase accepts an http or https URL with a host and nothing else but an
// optional clean path prefix.
func parseBase(raw string) (*url.URL, error) {
	bad := &Error{Code: "invalid_base"}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return nil, bad
	}
	if u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" {
		return nil, bad
	}
	if hasDotSegment(u.Path) || strings.ContainsAny(raw, " \t\r\n\\") {
		return nil, bad
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawPath = ""
	return u, nil
}

func hasDotSegment(p string) bool {
	for _, seg := range strings.Split(p, "/") {
		if seg == "." || seg == ".." {
			return true
		}
	}
	return false
}

// validPath accepts only a fixed absolute path made of unreserved characters:
// no query, no fragment, no dot segment, no encoding tricks.
func validPath(p string) bool {
	if len(p) < 2 || p[0] != '/' || strings.Contains(p, "//") || hasDotSegment(p) {
		return false
	}
	for i := 0; i < len(p); i++ {
		c := p[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '/' || c == '-' || c == '_' || c == '.':
		default:
			return false
		}
	}
	return true
}

// Get performs one bounded GET. A redirect, a transport failure, a deadline
// or an oversize body comes back as an *Error; any other status comes back in
// the Response.
func (c *Client) Get(ctx context.Context, req Request) (*Response, error) {
	if !validPath(req.Path) {
		return nil, &Error{Code: "invalid_path"}
	}
	timeout := capTimeout(req.Timeout, c.timeout)
	limit := c.maxBody
	if req.MaxBody > 0 {
		limit = capBody(req.MaxBody, HardMaxBody)
	}

	// The deadline covers waiting for a slot, the call and the body read.
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	select {
	case c.sem <- struct{}{}:
		defer func() { <-c.sem }()
	case <-ctx.Done():
		return nil, ctxError(ctx)
	}

	u := c.base
	u.Path += req.Path
	u.RawQuery = req.Query.Encode()
	hr, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), http.NoBody)
	if err != nil {
		return nil, &Error{Code: "invalid_path"}
	}
	hr.Header.Set("Accept", "application/json")
	hr.Header.Set("User-Agent", "vigil")

	resp, err := c.hc.Do(hr)
	if err != nil {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		return nil, classify(ctx, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return nil, &Error{Code: reasons.RedirectRefused, Status: resp.StatusCode}
	}
	if resp.ContentLength > limit {
		return nil, &Error{Code: reasons.BodyCap}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, classify(ctx, err)
	}
	if int64(len(body)) > limit {
		return nil, &Error{Code: reasons.BodyCap}
	}
	return &Response{
		Status:      resp.StatusCode,
		ContentType: resp.Header.Get("Content-Type"),
		Body:        body,
	}, nil
}

func ctxError(ctx context.Context) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return &Error{Code: reasons.Timeout}
	}
	return &Error{Code: reasons.Unreachable}
}

// classify maps a transport failure to a detail code. The error itself is
// dropped: it contains the URL.
func classify(ctx context.Context, err error) error {
	switch {
	case errors.Is(err, errRedirect):
		return &Error{Code: reasons.RedirectRefused}
	case errors.Is(err, context.DeadlineExceeded), errors.Is(ctx.Err(), context.DeadlineExceeded):
		return &Error{Code: reasons.Timeout}
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return &Error{Code: reasons.Timeout}
	}
	return &Error{Code: reasons.Unreachable}
}
