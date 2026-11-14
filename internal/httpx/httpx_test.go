package httpx

import (
	"bytes"
	"compress/gzip"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Hedronite/vigil/internal/reasons"
)

// logical is the address the Client believes it is talking to. It is a
// non-loopback name, because the standard library never proxies loopback
// targets, and a proxy test against 127.0.0.1 would pass for the wrong reason.
const logical = "backend.example.test:9090"

// newTestClient returns a Client whose logical backend address dials srv. Any
// other address (a proxy, say) dials for real.
func newTestClient(t *testing.T, srv *httptest.Server, cfg Config) *Client {
	t.Helper()
	cfg.Base = "http://" + logical
	target := srv.Listener.Addr().String()
	var d net.Dialer
	c, err := newClient(cfg, func(ctx context.Context, network, addr string) (net.Conn, error) {
		if addr == logical {
			addr = target
		}
		return d.DialContext(ctx, network, addr)
	})
	if err != nil {
		t.Fatalf("newClient: %v", err)
	}
	return c
}

func mustCode(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("error = nil, want code %q", want)
	}
	if got := Code(err); got != want {
		t.Fatalf("code = %q (%v), want %q", got, err, want)
	}
}

func TestGetHappyPath(t *testing.T) {
	var gotMethod, gotURI, gotAccept string
	var gotLen int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotURI, gotAccept, gotLen = r.Method, r.URL.RequestURI(), r.Header.Get("Accept"), r.ContentLength
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv, Config{})
	q := url.Values{"query": {`up{host="a b"}`}, "limit": {"3"}}
	resp, err := c.Get(context.Background(), Request{Path: "/api/v1/query", Query: q})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if resp.Status != 200 || string(resp.Body) != `{"ok":true}` || resp.ContentType != "application/json" {
		t.Errorf("resp = %+v", resp)
	}
	if gotMethod != "GET" {
		t.Errorf("method = %q, want GET", gotMethod)
	}
	if gotLen > 0 {
		t.Errorf("request carried a body of %d bytes", gotLen)
	}
	if gotAccept != "application/json" {
		t.Errorf("Accept = %q", gotAccept)
	}
	if want := "/api/v1/query?limit=3&query=up%7Bhost%3D%22a+b%22%7D"; gotURI != want {
		t.Errorf("request URI = %q, want %q", gotURI, want)
	}
}

func TestBasePathPrefix(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { got = r.URL.Path }))
	defer srv.Close()
	var d net.Dialer
	c, err := newClient(Config{Base: "http://" + logical + "/prefix/"}, func(ctx context.Context, n, a string) (net.Conn, error) {
		return d.DialContext(ctx, n, srv.Listener.Addr().String())
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Get(context.Background(), Request{Path: "/api/health"}); err != nil {
		t.Fatal(err)
	}
	if got != "/prefix/api/health" {
		t.Errorf("path = %q", got)
	}
}

func TestNon2xxComesBackAsResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		_, _ = w.Write([]byte(`{"status":"error","errorType":"bad_data"}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv, Config{})
	resp, err := c.Get(context.Background(), Request{Path: "/api/v1/query"})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if resp.Status != 400 || !strings.Contains(string(resp.Body), "bad_data") {
		t.Errorf("resp = %+v", resp)
	}
	mustCode(t, resp.Err(), reasons.HTTPStatus)
	ok := &Response{Status: 204}
	if ok.Err() != nil {
		t.Error("204 reported as an error")
	}
}

func TestRedirectsRefused(t *testing.T) {
	var landed int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&landed, 1)
	}))
	defer other.Close()
	for _, status := range []int{301, 302, 303, 307, 308} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, other.URL+"/landing", status)
		}))
		c := newTestClient(t, srv, Config{})
		resp, err := c.Get(context.Background(), Request{Path: "/api/health"})
		srv.Close()
		if resp != nil {
			t.Errorf("status %d: got a response, want none", status)
		}
		mustCode(t, err, reasons.RedirectRefused)
	}
	if n := atomic.LoadInt32(&landed); n != 0 {
		t.Errorf("a redirect was followed %d time(s)", n)
	}
}

func TestProxyEnvironmentIgnored(t *testing.T) {
	var viaProxy, direct int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&viaProxy, 1)
	}))
	defer proxy.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&direct, 1)
	}))
	defer srv.Close()
	for _, k := range []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy", "ALL_PROXY", "all_proxy"} {
		t.Setenv(k, proxy.URL)
	}
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")

	c := newTestClient(t, srv, Config{})
	if tr, ok := c.hc.Transport.(*http.Transport); !ok || tr.Proxy != nil {
		t.Fatalf("transport Proxy is set; want nil")
	}
	if _, err := c.Get(context.Background(), Request{Path: "/api/health"}); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if n := atomic.LoadInt32(&viaProxy); n != 0 {
		t.Errorf("proxy saw %d request(s); HTTP_PROXY must be ignored", n)
	}
	if n := atomic.LoadInt32(&direct); n != 1 {
		t.Errorf("backend saw %d request(s), want 1", n)
	}
}

func TestBodyCap(t *testing.T) {
	body := func(n int) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write(bytes.Repeat([]byte("a"), n))
		}
	}
	srv := httptest.NewServer(body(101))
	defer srv.Close()
	c := newTestClient(t, srv, Config{MaxBody: 100})
	_, err := c.Get(context.Background(), Request{Path: "/x"})
	mustCode(t, err, reasons.BodyCap)

	exact := httptest.NewServer(body(100))
	defer exact.Close()
	c = newTestClient(t, exact, Config{MaxBody: 100})
	resp, err := c.Get(context.Background(), Request{Path: "/x"})
	if err != nil || len(resp.Body) != 100 {
		t.Fatalf("exactly at the cap: resp=%v err=%v", resp, err)
	}

	// A per-call cap below the client default applies.
	_, err = c.Get(context.Background(), Request{Path: "/x", MaxBody: 10})
	mustCode(t, err, reasons.BodyCap)
}

func TestBodyCapWithoutContentLength(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fl := w.(http.Flusher)
		for i := 0; i < 20; i++ {
			_, _ = w.Write(bytes.Repeat([]byte("a"), 10))
			fl.Flush()
		}
	}))
	defer srv.Close()
	c := newTestClient(t, srv, Config{MaxBody: 100})
	_, err := c.Get(context.Background(), Request{Path: "/x"})
	mustCode(t, err, reasons.BodyCap)
}

// TestBodyCapRejectsDeclaredLengthEarly: a body that declares more than the
// cap is refused from the header, without waiting for the bytes.
func TestBodyCapRejectsDeclaredLengthEarly(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1073741824")
		w.WriteHeader(200)
		_, _ = w.Write([]byte("just a few bytes"))
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)
	c := newTestClient(t, srv, Config{MaxBody: 100, Timeout: 5 * time.Second})
	start := time.Now()
	_, err := c.Get(context.Background(), Request{Path: "/x"})
	mustCode(t, err, reasons.BodyCap)
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("declared length was not refused early: %v", d)
	}
}

// TestBodyCapStopsReading: an endless stream ends at the cap, not at the
// deadline.
func TestBodyCapStopsReading(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chunk := bytes.Repeat([]byte("a"), 1024)
		for r.Context().Err() == nil {
			if _, err := w.Write(chunk); err != nil {
				return
			}
			w.(http.Flusher).Flush()
		}
	}))
	defer srv.Close()
	c := newTestClient(t, srv, Config{MaxBody: 4096, Timeout: 5 * time.Second})
	start := time.Now()
	_, err := c.Get(context.Background(), Request{Path: "/x"})
	mustCode(t, err, reasons.BodyCap)
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("an endless body was read for %v", d)
	}
}

func TestBodyCapAppliesAfterDecompression(t *testing.T) {
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	_, _ = zw.Write(bytes.Repeat([]byte("0"), 1<<20))
	_ = zw.Close()
	if gz.Len() > 4096 {
		t.Fatalf("test input is not small enough: %d bytes", gz.Len())
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Encoding", "gzip")
		_, _ = w.Write(gz.Bytes())
	}))
	defer srv.Close()
	c := newTestClient(t, srv, Config{MaxBody: 16 << 10})
	_, err := c.Get(context.Background(), Request{Path: "/x"})
	mustCode(t, err, reasons.BodyCap)
}

func TestCeilings(t *testing.T) {
	if got := capBody(1<<40, HardMaxBody); got != HardMaxBody {
		t.Errorf("capBody = %d, want the ceiling", got)
	}
	if got := capBody(0, HardMaxBody); got != HardMaxBody {
		t.Errorf("capBody(0) = %d", got)
	}
	if got := capTimeout(time.Hour, DefaultTimeout); got != HardMaxTimeout {
		t.Errorf("capTimeout = %v, want the ceiling", got)
	}
	if got := capTimeout(0, DefaultTimeout); got != DefaultTimeout {
		t.Errorf("capTimeout(0) = %v", got)
	}
	if PromMaxBody != 8<<20 || GrafanaMaxBody != 16<<10 {
		t.Errorf("body caps changed: %d %d", PromMaxBody, GrafanaMaxBody)
	}
}

func TestDeadlineOnSlowHeaders(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)
	c := newTestClient(t, srv, Config{})
	start := time.Now()
	_, err := c.Get(context.Background(), Request{Path: "/x", Timeout: 100 * time.Millisecond})
	mustCode(t, err, reasons.Timeout)
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("deadline took %v", d)
	}
}

func TestDeadlineOnSlowBody(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte("partial"))
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)
	c := newTestClient(t, srv, Config{})
	start := time.Now()
	_, err := c.Get(context.Background(), Request{Path: "/x", Timeout: 100 * time.Millisecond})
	mustCode(t, err, reasons.Timeout)
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("deadline took %v", d)
	}
}

func TestClientDefaultDeadline(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)
	c := newTestClient(t, srv, Config{Timeout: 100 * time.Millisecond})
	_, err := c.Get(context.Background(), Request{Path: "/x"})
	mustCode(t, err, reasons.Timeout)
}

func TestTwoInFlight(t *testing.T) {
	var cur, peak int32
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&cur, 1)
		mu.Lock()
		if n > peak {
			peak = n
		}
		mu.Unlock()
		time.Sleep(80 * time.Millisecond)
		atomic.AddInt32(&cur, -1)
	}))
	defer srv.Close()
	c := newTestClient(t, srv, Config{Timeout: 5 * time.Second})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.Get(context.Background(), Request{Path: "/x"}); err != nil {
				t.Errorf("Get: %v", err)
			}
		}()
	}
	wg.Wait()
	mu.Lock()
	defer mu.Unlock()
	// The literal 2 is the product rule; comparing against the constant
	// would let a changed constant pass.
	if peak != 2 || MaxInFlight != 2 {
		t.Errorf("peak in flight = %d (MaxInFlight %d), want exactly 2", peak, MaxInFlight)
	}
}

func TestDeadlineCoversWaitingForSlot(t *testing.T) {
	release := make(chan struct{})
	var started sync.WaitGroup
	started.Add(2)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started.Done()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	c := newTestClient(t, srv, Config{Timeout: 5 * time.Second})
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = c.Get(context.Background(), Request{Path: "/x"})
		}()
	}
	started.Wait()
	start := time.Now()
	_, err := c.Get(context.Background(), Request{Path: "/x", Timeout: 100 * time.Millisecond})
	mustCode(t, err, reasons.Timeout)
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("waiting for a slot ignored the deadline: %v", d)
	}
	close(release)
	wg.Wait()
}

func TestSlotReleasedAfterFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no", 500)
	}))
	defer srv.Close()
	c := newTestClient(t, srv, Config{})
	for i := 0; i < 6; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_, err := c.Get(ctx, Request{Path: "/x"})
		cancel()
		if err != nil {
			t.Fatalf("call %d: %v (a slot leaked)", i, err)
		}
	}
}

func TestErrorsNeverCarryAddresses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	addr := srv.Listener.Addr().String()
	srv.Close() // nothing listens any more
	var d net.Dialer
	c, err := newClient(Config{Base: "http://" + logical}, func(ctx context.Context, n, a string) (net.Conn, error) {
		return d.DialContext(ctx, n, addr)
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Get(context.Background(), Request{Path: "/api/v1/targets"})
	mustCode(t, err, reasons.Unreachable)
	host, port, _ := net.SplitHostPort(addr)
	for _, leak := range []string{host, port, "example.test", "9090", "http", "/api"} {
		if strings.Contains(err.Error(), leak) {
			t.Errorf("error %q leaks %q", err.Error(), leak)
		}
	}
}

func TestInvalidBase(t *testing.T) {
	bad := []string{
		"", "localhost:9090", "ftp://example.test", "file:///etc/passwd", "http://",
		"http://user:pw@example.test", "http://example.test?x=1", "http://example.test#f",
		"http://example.test/a/../b", "http://example.test/./a", "http://example.test/a b",
		"http://exa mple.test", "//example.test", "http:example.test",
	}
	for _, b := range bad {
		if _, err := New(Config{Base: b}); err == nil {
			t.Errorf("New(%q) accepted", b)
		}
	}
	good := []string{"http://example.test", "https://example.test:8443", "http://[::1]:9090", "http://192.0.2.7:9090/p"}
	for _, b := range good {
		if _, err := New(Config{Base: b}); err != nil {
			t.Errorf("New(%q): %v", b, err)
		}
	}
}

func TestInvalidPaths(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("request reached the server for a bad path: %s", r.URL)
	}))
	defer srv.Close()
	c := newTestClient(t, srv, Config{})
	bad := []string{
		"", "/", "api/v1", "//evil.example.test/x", "/a/../b", "/a/./b", "/a?x=1", "/a#f",
		"/a b", "/a%2e%2e/b", "/a\\b", "/a\nb", "http://example.test/x", "/a//b",
	}
	for _, p := range bad {
		_, err := c.Get(context.Background(), Request{Path: p})
		if err == nil {
			t.Errorf("path %q accepted", p)
		}
	}
	good := []string{"/api/v1/targets", "/api/v1/query", "/-/ready", "/api/health"}
	for _, p := range good {
		if !validPath(p) {
			t.Errorf("validPath(%q) = false", p)
		}
	}
}
