//go:build unix

package simplexdoc_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/Hedronite/vigil/internal/reasons"
	"github.com/Hedronite/vigil/internal/simplexdoc"
)

func write(t *testing.T, body []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "simplex.json")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

// TestResolvePath: an absolute path or env:NAME holding one. Nothing relative,
// nothing with a dot segment, no URL.
func TestResolvePath(t *testing.T) {
	t.Setenv("VIGIL_TEST_DOC", "/var/lib/vigil/simplex.json")
	t.Setenv("VIGIL_TEST_SPACE", "/var/lib/a dir/simplex.json")
	for ref, want := range map[string]string{
		"/var/lib/vigil/simplex.json": "/var/lib/vigil/simplex.json",
		"/var/lib/vigil/..simplex":    "/var/lib/vigil/..simplex",
		"env:VIGIL_TEST_DOC":          "/var/lib/vigil/simplex.json",
		"env:VIGIL_TEST_SPACE":        "/var/lib/a dir/simplex.json",
	} {
		got, err := simplexdoc.ResolvePath(ref)
		if err != nil || got != want {
			t.Errorf("%q = %q, %v; want %q", ref, got, err, want)
		}
	}

	t.Setenv("VIGIL_TEST_EMPTY", "")
	t.Setenv("VIGIL_TEST_RELATIVE", "state/simplex.json")
	t.Setenv("VIGIL_TEST_DOTS", "/var/lib/../etc/shadow")
	t.Setenv("VIGIL_TEST_URL", "http://example.invalid/simplex.json")
	t.Setenv("VIGIL_TEST_CHAIN", "env:VIGIL_TEST_DOC")
	t.Setenv("lower_case", "/var/lib/vigil/simplex.json")
	for _, ref := range []string{
		"", "simplex.json", "./simplex.json", "~/simplex.json",
		"/var/lib/../etc/shadow", "/var/./simplex.json", "/..",
		"http://example.invalid/simplex.json", "file:///var/lib/simplex.json",
		"env:", "env:VIGIL_TEST_UNSET", "env:VIGIL_TEST_EMPTY", "env:VIGIL_TEST_RELATIVE",
		"env:VIGIL_TEST_DOTS", "env:VIGIL_TEST_URL", "env:VIGIL_TEST_CHAIN", "env:lower_case",
		"ENV:VIGIL_TEST_DOC",
	} {
		got, err := simplexdoc.ResolvePath(ref)
		if got != "" {
			t.Errorf("%q resolved to %q", ref, got)
		}
		wantUnavailable(t, err, reasons.SourceUnavailable)
	}
}

// TestReadFileBounds: exactly the cap is read, one byte more is refused.
func TestReadFileBounds(t *testing.T) {
	if simplexdoc.MaxBytes != 4<<20 {
		t.Fatalf("MaxBytes = %d, want 4 MiB", simplexdoc.MaxBytes)
	}
	doc := vendored(t, "green")
	full := append(doc, bytes.Repeat([]byte(" "), simplexdoc.MaxBytes-len(doc))...)
	body, _, err := simplexdoc.ReadFile(t.Context(), write(t, full))
	if err != nil || len(body) != simplexdoc.MaxBytes {
		t.Fatalf("at the cap: %d bytes, %v", len(body), err)
	}
	if _, err := simplexdoc.Decode(body); err != nil {
		t.Errorf("a padded document no longer decodes: %v", err)
	}
	body, _, err = simplexdoc.ReadFile(t.Context(), write(t, append(full, ' ')))
	if body != nil {
		t.Errorf("one byte over still returned %d bytes", len(body))
	}
	wantUnavailable(t, err, reasons.SourceUnavailable)
}

// TestReadFileRefusals: only a readable regular file is a document. A pipe
// must be refused without waiting for a writer.
func TestReadFileRefusals(t *testing.T) {
	dir := t.TempDir()
	pipe := filepath.Join(dir, "pipe")
	if err := syscall.Mkfifo(pipe, 0o600); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}
	cases := map[string]string{
		"missing":   filepath.Join(dir, "none.json"),
		"directory": dir,
		"pipe":      pipe,
		"device":    "/dev/null",
	}
	if os.Geteuid() != 0 {
		locked := write(t, vendored(t, "green"))
		if err := os.Chmod(locked, 0); err != nil {
			t.Fatalf("chmod: %v", err)
		}
		cases["unreadable"] = locked
	}
	for name, path := range cases {
		t.Run(name, func(t *testing.T) {
			done := make(chan error, 1)
			go func() {
				body, _, err := simplexdoc.ReadFile(context.Background(), path)
				if body != nil {
					err = errors.New("a body came back")
				}
				done <- err
			}()
			select {
			case err := <-done:
				wantUnavailable(t, err, reasons.SourceUnavailable)
			case <-time.After(10 * time.Second):
				t.Fatal("the read blocked")
			}
		})
	}
}

// TestReadCancelled: a cancelled call says so. That is not a statement about
// the source, so it is not an UnavailableError.
func TestReadCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	seen, err := simplexdoc.Read(ctx, write(t, vendored(t, "green")), time.Now())
	if seen != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("read = %v, %v; want context.Canceled", seen, err)
	}
	var gone *simplexdoc.UnavailableError
	if errors.As(err, &gone) {
		t.Errorf("cancellation reported as %s", gone.DetailCode)
	}
}

// TestReadSighting: received_at is the file's modification time in UTC, and
// never later than the caller's clock.
func TestReadSighting(t *testing.T) {
	path := write(t, vendored(t, "green"))
	modified := time.Date(2026, 11, 13, 18, 0, 5, 0, time.FixedZone("east", 3600))
	if err := os.Chtimes(path, modified, modified); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	link := filepath.Join(t.TempDir(), "link.json")
	if err := os.Symlink(path, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	t.Setenv("VIGIL_TEST_DOC", link)
	for name, tc := range map[string]struct{ now, want time.Time }{
		"the file is older": {now: modified.Add(time.Minute), want: modified},
		"same instant":      {now: modified, want: modified},
		"the file is ahead": {now: modified.Add(-time.Minute), want: modified.Add(-time.Minute)},
	} {
		seen, err := simplexdoc.Read(t.Context(), "env:VIGIL_TEST_DOC", tc.now)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !seen.ReceivedAt.Equal(tc.want) || seen.ReceivedAt.Location() != time.UTC {
			t.Errorf("%s: received_at = %v, want %v in UTC", name, seen.ReceivedAt, tc.want.UTC())
		}
	}
}

// TestReadPassesRefusalsOn: each stage's refusal reaches the caller unchanged.
func TestReadPassesRefusalsOn(t *testing.T) {
	for ref, code := range map[string]string{
		"relative.json":                                            reasons.SourceUnavailable,
		filepath.Join(t.TempDir(), "none.json"):                    reasons.SourceUnavailable,
		write(t, []byte("{")):                                      reasons.SourceUnavailable,
		write(t, []byte(`{"apiVersion":"simplex.probe/v1"}`)):      reasons.SchemaMajor,
		write(t, bytes.Repeat([]byte("1"), simplexdoc.MaxBytes+1)): reasons.SourceUnavailable,
	} {
		seen, err := simplexdoc.Read(t.Context(), ref, time.Now())
		if seen != nil {
			t.Errorf("%s: an observation came back", ref)
		}
		wantUnavailable(t, err, code)
	}
}
