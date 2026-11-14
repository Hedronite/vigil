package httpx

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Hedronite/vigil/internal/reasons"
)

// peakServer is a backend that records how many calls it served at once.
type peakServer struct {
	*httptest.Server
	cur, peak int32
	mu        sync.Mutex
}

func newPeakServer(t *testing.T) *peakServer {
	t.Helper()
	p := &peakServer{}
	p.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&p.cur, 1)
		p.mu.Lock()
		if n > p.peak {
			p.peak = n
		}
		p.mu.Unlock()
		time.Sleep(80 * time.Millisecond)
		atomic.AddInt32(&p.cur, -1)
	}))
	t.Cleanup(p.Close)
	return p
}

func (p *peakServer) peakSeen() int32 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.peak
}

// dialTo sends the logical backend address to srv.
func dialTo(srv *httptest.Server) dialFunc {
	var d net.Dialer
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		return d.DialContext(ctx, network, srv.Listener.Addr().String())
	}
}

func hammer(t *testing.T, calls int, get func() (*Client, error)) {
	t.Helper()
	var wg sync.WaitGroup
	for i := 0; i < calls; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Each call asks the registry again, as a command would.
			c, err := get()
			if err != nil {
				t.Errorf("ForBackend: %v", err)
				return
			}
			if _, err := c.Get(context.Background(), Request{Path: "/x", Timeout: 5 * time.Second}); err != nil {
				t.Errorf("Get: %v", err)
			}
		}()
	}
	wg.Wait()
}

func TestForBackendSharesOneSemaphorePerID(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	srv := newPeakServer(t)
	cfg := Config{Base: "http://" + logical}
	first, err := forBackend("prom", cfg, dialTo(srv.Server))
	if err != nil {
		t.Fatal(err)
	}
	second, err := forBackend("prom", cfg, dialTo(srv.Server))
	if err != nil {
		t.Fatal(err)
	}
	if first != second || first.sem != second.sem {
		t.Fatal("two calls with one id returned different clients")
	}
	// Nine concurrent calls, each resolving the client afresh.
	hammer(t, 9, func() (*Client, error) { return forBackend("prom", cfg, dialTo(srv.Server)) })
	// The literal 2 is the product rule; the constant could be changed along
	// with the code.
	if got := srv.peakSeen(); got != 2 || MaxInFlight != 2 {
		t.Errorf("peak in flight for one backend = %d (MaxInFlight %d), want exactly 2", got, MaxInFlight)
	}
}

func TestForBackendDoesNotShareAcrossIDs(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	a, b := newPeakServer(t), newPeakServer(t)
	cfg := Config{Base: "http://" + logical}
	ca, err := forBackend("prom", cfg, dialTo(a.Server))
	if err != nil {
		t.Fatal(err)
	}
	cb, err := forBackend("grafana", cfg, dialTo(b.Server))
	if err != nil {
		t.Fatal(err)
	}
	if ca == cb || ca.sem == cb.sem {
		t.Fatal("two ids share a client or a semaphore")
	}
	var wg sync.WaitGroup
	for _, tc := range []struct {
		id  string
		srv *peakServer
	}{{"prom", a}, {"grafana", b}} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			hammer(t, 6, func() (*Client, error) { return forBackend(tc.id, cfg, nil) })
		}()
	}
	wg.Wait()
	// Each backend is held to two; neither one throttles the other.
	if a.peakSeen() != 2 || b.peakSeen() != 2 {
		t.Errorf("peaks = %d and %d, want 2 and 2", a.peakSeen(), b.peakSeen())
	}
}

func TestForBackendFirstConfigWins(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	first, err := ForBackend("prom", Config{Base: "http://" + logical, MaxBody: 100, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	second, err := ForBackend("prom", Config{Base: "http://other.example.test:1", MaxBody: 5, Timeout: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatal("a second config built a second client")
	}
	if second.base.Host != logical || second.maxBody != 100 || second.timeout != time.Second {
		t.Errorf("the first config did not win: host=%s body=%d timeout=%v", second.base.Host, second.maxBody, second.timeout)
	}
}

func TestForBackendIsSafeForConcurrentUse(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	const n = 64
	got := make([]*Client, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			c, err := ForBackend("prom", Config{Base: "http://" + logical})
			if err != nil {
				t.Errorf("ForBackend: %v", err)
				return
			}
			got[i] = c
		}()
	}
	close(start)
	wg.Wait()
	for i, c := range got {
		if c != got[0] {
			t.Fatalf("goroutine %d got a different client", i)
		}
	}
}

func TestForBackendDoesNotRememberFailures(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	if _, err := ForBackend("prom", Config{Base: "not a url"}); err == nil {
		t.Fatal("a bad base was accepted")
	}
	c, err := ForBackend("prom", Config{Base: "http://" + logical})
	if err != nil || c == nil {
		t.Fatalf("a good config after a bad one failed: %v", err)
	}
	if _, err := ForBackend("", Config{Base: "http://" + logical}); err == nil {
		t.Error("an empty id was accepted")
	}
}

// TestForBackendKeepsTheClientRules: a shared client is still the bounded one.
func TestForBackendKeepsTheClientRules(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://other.example.test/", http.StatusFound)
	}))
	defer srv.Close()
	c, err := forBackend("prom", Config{Base: "http://" + logical}, dialTo(srv))
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Get(context.Background(), Request{Path: "/x"})
	mustCode(t, err, reasons.RedirectRefused)
}
