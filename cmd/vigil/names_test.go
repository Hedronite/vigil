package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The plaintext list of names this guard rejects is held by maintainers
// off-repo. The lists below hold salted hashes, which stop the names from being
// published in plaintext; they do not stop someone guessing a short word.
var (
	wholeWordHashes = []string{
		"726801e825029634be30eafaffa2b51240198b3ac23b66ec6ce8afca325985e2",
	}
	anywhereHashes = []string{
		"00a53508cca2456ef26145539629c01779bd0d90e22ef992c43c5843ed90e6de",
		"50bfe829ec9c7224f5d2ebd78ec23d81ad27da4144c6d2496752f02147017bff",
		"5aa1bb0724dcf4e4bdc31b21387cee6067530e2df0067acb7e941df4b097c753",
		"98c9243625169750285750f2698acc1602bf97b14cc0b0ae562ce245810ae738",
		"d2a514ff0ce4f3dfabd379f94fa495f3fc083a00f064cf1aa26ed379f2f03b59",
		"f29161d0e89f704fc26766a6e9e464c9656ba27ab79036204c872c6efde03307",
	}
)

const (
	nameSalt        = "vigil-names/v0:"
	plantedAlphaSum = "542f64caf1fea8ff3ff9fbdf6e383a829e0d7fd357ed53df7924df528b1e54b1"
	plantedBetaSum  = "a28a010c19624a3b6f7f8656fc6d85cdb803b802c7f0a9e12b35b29f9d5343ff"
)

func nameHash(s string) string {
	sum := sha256.Sum256([]byte(nameSalt + s))
	return hex.EncodeToString(sum[:])
}

type nameHit struct {
	list  string
	index int
}

type nameGuard struct {
	whole, anywhere  map[string]int
	minLen, maxLen   int
	tokenMemo        map[string][]nameHit
	substringMemoHit map[string]int
}

func newNameGuard(whole, anywhere []string, minLen, maxLen int) *nameGuard {
	g := &nameGuard{
		whole:            map[string]int{},
		anywhere:         map[string]int{},
		minLen:           minLen,
		maxLen:           maxLen,
		tokenMemo:        map[string][]nameHit{},
		substringMemoHit: map[string]int{},
	}
	for i, h := range whole {
		g.whole[h] = i
	}
	for i, h := range anywhere {
		g.anywhere[h] = i
	}
	return g
}

func nameTokens(b []byte) []string {
	var out []string
	start := -1
	for i := 0; i <= len(b); i++ {
		var c byte
		in := false
		if i < len(b) {
			c = b[i]
			if c >= 'A' && c <= 'Z' {
				c += 'a' - 'A'
			}
			in = (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')
		}
		if in && start < 0 {
			start = i
		}
		if !in && start >= 0 {
			tok := make([]byte, i-start)
			for j := range tok {
				c := b[start+j]
				if c >= 'A' && c <= 'Z' {
					c += 'a' - 'A'
				}
				tok[j] = c
			}
			out = append(out, string(tok))
			start = -1
		}
	}
	return out
}

func (g *nameGuard) tokenHits(tok string) []nameHit {
	if h, ok := g.tokenMemo[tok]; ok {
		return h
	}
	var hits []nameHit
	if i, ok := g.whole[nameHash(tok)]; ok {
		hits = append(hits, nameHit{"wholeWord", i})
	}
	for l := g.minLen; l <= g.maxLen && l <= len(tok); l++ {
		for s := 0; s+l <= len(tok); s++ {
			sub := tok[s : s+l]
			i, seen := g.substringMemoHit[sub]
			if !seen {
				i = -1
				if j, ok := g.anywhere[nameHash(sub)]; ok {
					i = j
				}
				g.substringMemoHit[sub] = i
			}
			if i >= 0 {
				hits = append(hits, nameHit{"anywhere", i})
			}
		}
	}
	g.tokenMemo[tok] = hits
	return hits
}

func (g *nameGuard) lineHits(b []byte) []nameHit {
	var hits []nameHit
	seen := map[nameHit]bool{}
	for _, tok := range nameTokens(b) {
		for _, h := range g.tokenHits(tok) {
			if !seen[h] {
				seen[h] = true
				hits = append(hits, h)
			}
		}
	}
	return hits
}

func (g *nameGuard) scanTree(root string) []string {
	var msgs []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Name() == ".git" {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		parts := strings.Split(filepath.ToSlash(rel), "/")
		masked := make([]string, len(parts))
		var pathHits []nameHit
		for i, part := range parts {
			masked[i] = part
			if h := g.lineHits([]byte(part)); len(h) > 0 {
				masked[i] = "*"
				pathHits = append(pathHits, h...)
			}
		}
		shown := strings.Join(masked, "/")
		for _, h := range pathHits {
			msgs = append(msgs, fmt.Sprintf("%s:0: denylisted name (list %s, index %d)", shown, h.list, h.index))
		}
		data, err := os.ReadFile(p)
		if err != nil {
			msgs = append(msgs, fmt.Sprintf("%s:0: unreadable", shown))
			return nil
		}
		for n, line := range splitLines(data) {
			for _, h := range g.lineHits(line) {
				msgs = append(msgs, fmt.Sprintf("%s:%d: denylisted name (list %s, index %d)", shown, n+1, h.list, h.index))
			}
		}
		return nil
	})
	if err != nil {
		msgs = append(msgs, "walk failed")
	}
	return msgs
}

func splitLines(data []byte) [][]byte {
	var lines [][]byte
	start := 0
	for i, c := range data {
		if c == '\n' {
			lines = append(lines, data[start:i])
			start = i + 1
		}
	}
	return append(lines, data[start:])
}

func nameGuardRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above the package directory")
		}
		dir = parent
	}
}

func TestNoDenylistedNamesInTree(t *testing.T) {
	g := newNameGuard(wholeWordHashes, anywhereHashes, 5, 7)
	if msgs := g.scanTree(nameGuardRoot(t)); len(msgs) > 0 {
		t.Fatalf("%d denylisted names:\n%s", len(msgs), strings.Join(msgs, "\n"))
	}
}

func plant(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, body := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestNameGuardBites(t *testing.T) {
	if nameHash("zzplantedalpha") != plantedAlphaSum || nameHash("zzplantedbeta") != plantedBetaSum {
		t.Fatal("the planted hashes do not match the salt")
	}
	g := func() *nameGuard {
		return newNameGuard([]string{plantedBetaSum}, []string{plantedAlphaSum}, 14, 14)
	}
	flagged := map[string]map[string]string{
		"alone":           {"a.txt": "one\ntwo\nzzplantedalpha\n"},
		"case":            {"a.txt": "ZZPlantedAlpha\n"},
		"substring":       {"a.txt": "xzzplantedalphay\n"},
		"joined":          {"a.txt": "x-zzplantedbeta-1\n"},
		"file name":       {"zzplantedbeta.txt": "clean\n"},
		"dir name":        {"zzplantedalpha-dir/ok.txt": "clean\n"},
		"nested name":     {"a/b/xzzplantedalphay.go": "clean\n"},
		"upper case line": {"deep/er/a.txt": "ok\nZZPLANTEDBETA\n"},
	}
	for name, files := range flagged {
		t.Run("flags "+name, func(t *testing.T) {
			msgs := g().scanTree(plant(t, files))
			if len(msgs) == 0 {
				t.Fatal("not flagged")
			}
			for _, m := range msgs {
				if strings.Contains(strings.ToLower(m), "zzplanted") {
					t.Fatalf("a failure message carries planted text: %s", m)
				}
				if !strings.Contains(m, "denylisted name (list ") {
					t.Fatalf("unexpected message shape: %s", m)
				}
			}
		})
	}
	t.Run("reports the line", func(t *testing.T) {
		msgs := g().scanTree(plant(t, flagged["alone"]))
		if len(msgs) != 1 || msgs[0] != "a.txt:3: denylisted name (list anywhere, index 0)" {
			t.Fatalf("got %q", msgs)
		}
	})
	clean := map[string]map[string]string{
		"whole word has no substring match": {"a.txt": "zzplantedbetax\nxzzplantedbeta\n"},
		"clean file name":                   {"zzplantedbetax.txt": "clean\n"},
		"git metadata is skipped":           {".git/config": "zzplantedalpha\n", "a.txt": "fine\n"},
		"ordinary text":                     {"a.txt": "sandbox checkbox\n"},
	}
	for name, files := range clean {
		t.Run("passes "+name, func(t *testing.T) {
			if msgs := g().scanTree(plant(t, files)); len(msgs) != 0 {
				t.Fatalf("flagged: %q", msgs)
			}
		})
	}
}

func TestNameGuardSubstringLengths(t *testing.T) {
	in := []string{nameHash("zzqxj"), nameHash("zzqxjw"), nameHash("zzqxjwv")}
	out := []string{nameHash("yyqx"), nameHash("yyqxjwvk")}
	for _, text := range []string{"aazzqxjbb", "aazzqxjwbb", "aazzqxjwvbb"} {
		g := newNameGuard(nil, in, 5, 7)
		if msgs := g.scanTree(plant(t, map[string]string{"a.txt": text + "\n"})); len(msgs) == 0 {
			t.Fatalf("%q was not flagged", text)
		}
	}
	for _, text := range []string{"aayyqxbb", "aayyqxjwvkbb"} {
		g := newNameGuard(nil, out, 5, 7)
		if msgs := g.scanTree(plant(t, map[string]string{"a.txt": text + "\n"})); len(msgs) != 0 {
			t.Fatalf("%q was flagged outside the 5 to 7 range: %q", text, msgs)
		}
	}
}
