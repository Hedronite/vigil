// Package config loads the two documents Vigil is configured with:
// vigil.config/v0, which names the backends, the authorities, the tolerance for
// stale documents and the named queries, and vigil.overlay/v0, which holds the
// addresses and the host mapping and is never tracked in the repository.
//
// The loader is strict: an unknown key, an unknown major, a duplicate id or a
// malformed value is an error, because a typo that silently drops a backend
// would otherwise read as a healthy mesh with nothing in it. The Go checks
// mirror schema/, and the fixture tests prove the two agree; the binary never
// runs the JSON Schema validator.
package config

import (
	"fmt"
	"net/netip"
	"net/url"
	"os"
	"regexp"
	"strings"

	"github.com/Hedronite/vigil/internal/exitcode"
	"go.yaml.in/yaml/v3"
)

// API versions and defaults.
const (
	ConfigMajor   = "vigil.config/v0"
	OverlayMajor  = "vigil.overlay/v0"
	DefaultListen = "127.0.0.1:10082"
	// DefaultQueryTimeout and DefaultMaxSeries apply when a named query leaves
	// them out. The maxima are the contract's limits, not preferences.
	DefaultQueryTimeout = "5s"
	DefaultMaxSeries    = 200
	MaxQueryTimeout     = "10s"
	maxSeriesCap        = 200
	maxExprLength       = 2048
)

// Error is a configuration failure that already knows its exit code: a bad
// document is a usage error (3), an unspecified listen address is a startup
// refusal (5).
type Error struct {
	Code  int
	File  string
	Field string
	Msg   string
}

func (e *Error) Error() string {
	var b strings.Builder
	b.WriteString(e.File)
	if e.Field != "" {
		b.WriteString(": ")
		b.WriteString(e.Field)
	}
	b.WriteString(": ")
	b.WriteString(e.Msg)
	return b.String()
}

func usageErr(file, field, format string, a ...any) *Error {
	return &Error{Code: exitcode.Usage, File: file, Field: field, Msg: fmt.Sprintf(format, a...)}
}

func startupErr(file, field, format string, a ...any) *Error {
	return &Error{Code: exitcode.Startup, File: file, Field: field, Msg: fmt.Sprintf(format, a...)}
}

// Config is vigil.config/v0 with defaults applied.
type Config struct {
	APIVersion  string      `yaml:"apiVersion"`
	Listen      string      `yaml:"listen"`
	Backends    []Backend   `yaml:"backends"`
	Authorities []Authority `yaml:"authorities"`
	Sources     []Source    `yaml:"sources"`
	Queries     []Query     `yaml:"queries"`
	Freshness   *Freshness  `yaml:"freshness"`
	Tokens      *Tokens     `yaml:"tokens"`

	file string
}

// Backend is one named backend. It has no address: the address lives in the
// overlay, keyed by Id.
type Backend struct {
	ID      string `yaml:"id"`
	Kind    string `yaml:"kind"`
	Vantage string `yaml:"vantage"`
}

// Authority is a document whose own severity is combined into the summary.
type Authority struct {
	ID         string `yaml:"id"`
	PathRef    string `yaml:"path_ref"`
	StaleAfter string `yaml:"stale_after"`
}

// Source is an informational document that never colours the summary.
type Source struct {
	ID      string `yaml:"id"`
	PathRef string `yaml:"path_ref"`
}

// Query is a named PromQL query. Expr may contain only the literal {{.host}}.
type Query struct {
	Name      string `yaml:"name"`
	Backend   string `yaml:"backend"`
	Expr      string `yaml:"expr"`
	Timeout   string `yaml:"timeout"`
	MaxSeries int    `yaml:"max_series"`
}

// Freshness holds the global age bound for authority documents.
type Freshness struct {
	StaleAfter string `yaml:"stale_after"`
}

// Tokens holds the paths to the read and signal tokens.
type Tokens struct {
	ReadFile   string `yaml:"read_file"`
	SignalFile string `yaml:"signal_file"`
}

// Overlay is vigil.overlay/v0: the only place an address exists, plus the map
// from a target instance label to a public slug.
type Overlay struct {
	APIVersion string           `yaml:"apiVersion"`
	Backends   []OverlayBackend `yaml:"backends"`
	Hosts      []OverlayHost    `yaml:"hosts"`

	file string
}

// OverlayBackend binds a backend id from the config to an address.
type OverlayBackend struct {
	ID      string `yaml:"id"`
	Address string `yaml:"address"`
}

// OverlayHost maps the instance labels a backend reports onto one public slug.
type OverlayHost struct {
	Slug      string   `yaml:"slug"`
	Instances []string `yaml:"instances"`
}

var (
	slugPattern     = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
	durationPattern = regexp.MustCompile(`^[1-9][0-9]{0,3}s$`)
	timeoutPattern  = regexp.MustCompile(`^([1-9]|10)s$`)
	// exprPattern permits braces only as the literal {{.host}}; a label
	// selector such as up{job="node"} still passes.
	exprPattern     = regexp.MustCompile(`^([^{]|\{[^{]|\{\{\.host\}\}|\{$)*$`)
	locatorPattern  = regexp.MustCompile(`^(env:[A-Z][A-Z0-9_]{1,63}|/[A-Za-z0-9._/-]{1,255})$`)
	instancePattern = regexp.MustCompile(`^[A-Za-z0-9._:\[\]-]{1,255}$`)
	jobPattern      = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)
)

// Load reads and validates vigil.config/v0.
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, usageErr(path, "", "read config: %v", err)
	}
	var c Config
	dec := yaml.NewDecoder(strings.NewReader(string(raw)))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return nil, usageErr(path, "", "decode config: %v", err)
	}
	c.file = path
	if err := c.validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *Config) validate() error {
	if c.APIVersion != ConfigMajor {
		return usageErr(c.file, "apiVersion", "want %q, got %q", ConfigMajor, c.APIVersion)
	}
	if c.Listen == "" {
		c.Listen = DefaultListen
	}
	if err := checkListen(c.file, c.Listen); err != nil {
		return err
	}

	backendIDs := map[string]bool{}
	for i, b := range c.Backends {
		field := fmt.Sprintf("backends[%d]", i)
		if !slugPattern.MatchString(b.ID) {
			return usageErr(c.file, field+".id", "%q is not a slug", b.ID)
		}
		if backendIDs[b.ID] {
			return usageErr(c.file, field+".id", "duplicate id %q", b.ID)
		}
		backendIDs[b.ID] = true
		switch b.Kind {
		case "prom", "grafana":
		default:
			return usageErr(c.file, field+".kind", "want prom or grafana, got %q", b.Kind)
		}
		if b.Vantage != "" && !slugPattern.MatchString(b.Vantage) {
			return usageErr(c.file, field+".vantage", "%q is not a slug", b.Vantage)
		}
	}

	if len(c.Authorities) == 0 {
		return usageErr(c.file, "authorities", "at least one authority is required")
	}
	authorityIDs := map[string]bool{}
	for i, a := range c.Authorities {
		field := fmt.Sprintf("authorities[%d]", i)
		switch a.ID {
		case "simplex", "augur":
		default:
			return usageErr(c.file, field+".id", "want simplex or augur, got %q", a.ID)
		}
		if authorityIDs[a.ID] {
			return usageErr(c.file, field+".id", "duplicate id %q", a.ID)
		}
		authorityIDs[a.ID] = true
		if err := checkLocator(c.file, field+".path_ref", a.PathRef); err != nil {
			return err
		}
		if a.StaleAfter != "" && !durationPattern.MatchString(a.StaleAfter) {
			return usageErr(c.file, field+".stale_after", "%q is not a duration in seconds", a.StaleAfter)
		}
	}

	sourceIDs := map[string]bool{}
	for i, s := range c.Sources {
		field := fmt.Sprintf("sources[%d]", i)
		switch s.ID {
		case "scry", "cubit":
		default:
			return usageErr(c.file, field+".id", "want scry or cubit, got %q", s.ID)
		}
		if sourceIDs[s.ID] {
			return usageErr(c.file, field+".id", "duplicate id %q", s.ID)
		}
		sourceIDs[s.ID] = true
		if err := checkLocator(c.file, field+".path_ref", s.PathRef); err != nil {
			return err
		}
	}

	if c.Freshness != nil && c.Freshness.StaleAfter != "" && !durationPattern.MatchString(c.Freshness.StaleAfter) {
		return usageErr(c.file, "freshness.stale_after", "%q is not a duration in seconds", c.Freshness.StaleAfter)
	}

	names := map[string]bool{}
	for i := range c.Queries {
		q := &c.Queries[i]
		field := fmt.Sprintf("queries[%d]", i)
		if !jobPattern.MatchString(q.Name) {
			return usageErr(c.file, field+".name", "%q is not a name", q.Name)
		}
		if names[q.Name] {
			return usageErr(c.file, field+".name", "duplicate name %q", q.Name)
		}
		names[q.Name] = true
		if !backendIDs[q.Backend] {
			return usageErr(c.file, field+".backend", "no backend %q", q.Backend)
		}
		if len(q.Expr) == 0 || len(q.Expr) > maxExprLength {
			return usageErr(c.file, field+".expr", "want 1 to %d bytes", maxExprLength)
		}
		if strings.ContainsAny(q.Expr, "\r\n") {
			return usageErr(c.file, field+".expr", "must be a single line")
		}
		if !exprPattern.MatchString(q.Expr) {
			return usageErr(c.file, field+".expr", "only the literal {{.host}} may appear in braces")
		}
		if q.Timeout == "" {
			q.Timeout = DefaultQueryTimeout
		}
		if !timeoutPattern.MatchString(q.Timeout) {
			return usageErr(c.file, field+".timeout", "want 1s to %s, got %q", MaxQueryTimeout, q.Timeout)
		}
		if q.MaxSeries == 0 {
			q.MaxSeries = DefaultMaxSeries
		}
		if q.MaxSeries < 1 || q.MaxSeries > maxSeriesCap {
			return usageErr(c.file, field+".max_series", "want 1 to %d, got %d", maxSeriesCap, q.MaxSeries)
		}
	}

	if c.Tokens != nil {
		if c.Tokens.ReadFile != "" {
			if err := checkLocator(c.file, "tokens.read_file", c.Tokens.ReadFile); err != nil {
				return err
			}
		}
		if c.Tokens.SignalFile != "" {
			if err := checkLocator(c.file, "tokens.signal_file", c.Tokens.SignalFile); err != nil {
				return err
			}
		}
	}
	return nil
}

// checkListen rejects every spelling of a wildcard. netip decides whether an
// address is unspecified, which covers 0.0.0.0, ::, every zero-spelled IPv6
// form and the IPv4-mapped forms; a value with no host at all is the remaining
// spelling. A wildcard is a refusal to start (5), not a typo (3).
func checkListen(file, value string) error {
	if strings.HasPrefix(value, ":") || value == "" {
		return startupErr(file, "listen", "a wildcard address is refused; bind one address")
	}
	ap, err := netip.ParseAddrPort(value)
	if err != nil {
		return usageErr(file, "listen", "want ip:port, got %q", value)
	}
	if ap.Addr().IsUnspecified() || (ap.Addr().Is4In6() && ap.Addr().Unmap().IsUnspecified()) {
		return startupErr(file, "listen", "a wildcard address is refused; bind one address")
	}
	if ap.Port() == 0 {
		return usageErr(file, "listen", "port 0 is not a listen port")
	}
	return nil
}

// checkLocator accepts an environment indirection or an absolute path. A URL is
// not a locator, and no segment may be "." or "..": the path is read as given,
// so a traversal would step outside the file the operator named.
func checkLocator(file, field, value string) error {
	if !locatorPattern.MatchString(value) {
		return usageErr(file, field, "want env:NAME or an absolute path, got %q", value)
	}
	if strings.HasPrefix(value, "env:") {
		return nil
	}
	for _, seg := range strings.Split(value, "/") {
		if seg == "." || seg == ".." {
			return usageErr(file, field, "path segments may not be . or ..")
		}
	}
	return nil
}

// LoadOverlay reads and validates vigil.overlay/v0.
func LoadOverlay(path string) (*Overlay, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, usageErr(path, "", "read overlay: %v", err)
	}
	var o Overlay
	dec := yaml.NewDecoder(strings.NewReader(string(raw)))
	dec.KnownFields(true)
	if err := dec.Decode(&o); err != nil {
		return nil, usageErr(path, "", "decode overlay: %v", err)
	}
	o.file = path
	if err := o.validate(); err != nil {
		return nil, err
	}
	return &o, nil
}

func (o *Overlay) validate() error {
	if o.APIVersion != OverlayMajor {
		return usageErr(o.file, "apiVersion", "want %q, got %q", OverlayMajor, o.APIVersion)
	}
	ids := map[string]bool{}
	for i, b := range o.Backends {
		field := fmt.Sprintf("backends[%d]", i)
		if !slugPattern.MatchString(b.ID) {
			return usageErr(o.file, field+".id", "%q is not a slug", b.ID)
		}
		if ids[b.ID] {
			return usageErr(o.file, field+".id", "duplicate id %q", b.ID)
		}
		ids[b.ID] = true
		if err := checkAddress(o.file, field+".address", b.Address); err != nil {
			return err
		}
	}

	slugs := map[string]bool{}
	instances := map[string]string{}
	for i, h := range o.Hosts {
		field := fmt.Sprintf("hosts[%d]", i)
		if !slugPattern.MatchString(h.Slug) {
			return usageErr(o.file, field+".slug", "%q is not a slug", h.Slug)
		}
		if slugs[h.Slug] {
			return usageErr(o.file, field+".slug", "duplicate slug %q", h.Slug)
		}
		slugs[h.Slug] = true
		if len(h.Instances) == 0 {
			return usageErr(o.file, field+".instances", "at least one instance label is required")
		}
		for j, inst := range h.Instances {
			ifail := fmt.Sprintf("%s.instances[%d]", field, j)
			if !instancePattern.MatchString(inst) {
				return usageErr(o.file, ifail, "%q is not an instance label", inst)
			}
			if owner, ok := instances[inst]; ok {
				return usageErr(o.file, ifail, "instance %q already maps to %q", inst, owner)
			}
			instances[inst] = h.Slug
		}
	}
	return nil
}

// checkAddress accepts an http or https base URL with no credentials, query or
// fragment, and no dot segments: the base is joined with fixed paths, so a
// traversal in it would escape the prefix the operator wrote.
func checkAddress(file, field, value string) error {
	u, err := url.Parse(value)
	if err != nil {
		return usageErr(file, field, "not a URL: %v", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return usageErr(file, field, "want http or https, got %q", u.Scheme)
	}
	if u.Host == "" {
		return usageErr(file, field, "no host")
	}
	if u.User != nil {
		return usageErr(file, field, "credentials are not allowed in an address")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return usageErr(file, field, "no query or fragment")
	}
	for _, seg := range strings.Split(u.Path, "/") {
		if seg == "." || seg == ".." {
			return usageErr(file, field, "path segments may not be . or ..")
		}
	}
	return nil
}

// Backend returns the named backend, if the config declares it.
func (c *Config) Backend(id string) (Backend, bool) {
	for _, b := range c.Backends {
		if b.ID == id {
			return b, true
		}
	}
	return Backend{}, false
}

// Query returns the named query, if the config declares it.
func (c *Config) Query(name string) (Query, bool) {
	for _, q := range c.Queries {
		if q.Name == name {
			return q, true
		}
	}
	return Query{}, false
}

// Address returns the address bound to a backend id.
func (o *Overlay) Address(id string) (string, bool) {
	for _, b := range o.Backends {
		if b.ID == id {
			return b.Address, true
		}
	}
	return "", false
}

// SlugFor maps a target instance label to its public slug.
func (o *Overlay) SlugFor(instance string) (string, bool) {
	for _, h := range o.Hosts {
		for _, inst := range h.Instances {
			if inst == instance {
				return h.Slug, true
			}
		}
	}
	return "", false
}

// Bind checks the cross-document references: every overlay address names a
// backend the config declares, and every queried backend has an address.
func (o *Overlay) Bind(c *Config) error {
	for _, b := range o.Backends {
		if _, ok := c.Backend(b.ID); !ok {
			return usageErr(o.file, "backends", "no backend %q in the config", b.ID)
		}
	}
	for _, q := range c.Queries {
		if _, ok := o.Address(q.Backend); !ok {
			return usageErr(c.file, "queries."+q.Name+".backend", "backend %q has no address in the overlay", q.Backend)
		}
	}
	return nil
}
