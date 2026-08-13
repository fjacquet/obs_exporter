// Package config loads and validates the exporter configuration.
package config

import (
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v2"
)

// Cluster is one Dell ECS cluster (VDC) to monitor.
type Cluster struct {
	Name               string  `yaml:"name"`
	Host               string  `yaml:"host"`
	MgmtPort           int     `yaml:"mgmtPort"` // management API, defaults to 4443
	ObjPort            int     `yaml:"objPort"`  // object/S3 API (DT ping), defaults to 9021
	DTPort             int     `yaml:"dtPort"`   // node-local DT stats, defaults to 9101
	Username           string  `yaml:"username"`
	Password           string  `yaml:"password"`
	PasswordFile       string  `yaml:"passwordFile"`
	InsecureSkipVerify EnvBool `yaml:"insecureSkipVerify"`
	// CollectDT opts in to scraping the undocumented node-local DT stats
	// (http://<node>:9101/stats/dt/DTInitStat and https://<node>:9021/?ping).
	CollectDT bool `yaml:"collectDT"`
	// CollectFlux opts in to querying the cluster's Flux/InfluxDB monitoring
	// store (POST /flux/api/external/v2/query on the management port) for metric
	// families the management API does not serve. Requires the cluster account to
	// hold SYSTEM_MONITOR or SYSTEM_ADMIN. Off by default: it adds a second data
	// protocol and a dependency on an ObjectScale internal.
	CollectFlux bool `yaml:"collectFlux"`
	// CollectMetering enables the namespace quota/billing collector (default true).
	// Billing can be slow on large clusters; disable it (or raise
	// collection.interval) if needed. Pointer so "unset" defaults to enabled.
	CollectMetering *bool `yaml:"collectMetering"`
	// CollectQuotas enables the per-namespace quota fetch inside the metering
	// collector (default true). The management API has no bulk quota endpoint, so
	// this costs one request per namespace per cycle — the only part of metering
	// that scales with namespace count. Turning it off keeps namespace usage
	// (one bulk billing POST) while dropping the quota metrics. Pointer so
	// "unset" defaults to enabled.
	CollectQuotas *bool `yaml:"collectQuotas"`
	// Labels holds this cluster's overrides for the globally declared custom
	// label values. A key absent from the top-level labels block is a config
	// error: the key set stays uniform across clusters (ADR-0006), only values
	// vary.
	Labels map[string]string `yaml:"labels"`
}

// enabled reads a default-true flag: unset (nil) means on, so a collector can be
// added without every existing config file having to opt into it.
func enabled(flag *bool) bool { return flag == nil || *flag }

// MeteringEnabled reports whether the metering collector should run.
func (c Cluster) MeteringEnabled() bool { return enabled(c.CollectMetering) }

// QuotasEnabled reports whether metering should fetch per-namespace quotas.
func (c Cluster) QuotasEnabled() bool { return enabled(c.CollectQuotas) }

// BaseURL returns the https://host:port root of the ECS management API.
func (c Cluster) BaseURL() string {
	return fmt.Sprintf("https://%s:%d", c.Host, c.MgmtPort)
}

// Server holds HTTP-server settings.
type Server struct {
	Host    string `yaml:"host"`
	Port    string `yaml:"port"`
	URI     string `yaml:"uri"`
	LogName string `yaml:"logName"`
}

// Collection holds loop timing.
type Collection struct {
	Interval time.Duration `yaml:"interval"`
	Timeout  time.Duration `yaml:"timeout"`
}

// OTLP holds the optional OTLP metric push settings. An empty endpoint disables it.
type OTLP struct {
	Endpoint string        `yaml:"endpoint"`
	Insecure bool          `yaml:"insecure"`
	Interval time.Duration `yaml:"interval"`
}

// Config is the whole file.
type Config struct {
	Server     Server     `yaml:"server"`
	Collection Collection `yaml:"collection"`
	OTLP       OTLP       `yaml:"otlp"`
	// Labels declares the custom label KEYS with their default values. Clusters
	// may override a value; they may never add a key. Declaring the key set once
	// is what keeps "no value is ever empty" true by construction.
	Labels   map[string]string `yaml:"labels"`
	Clusters []Cluster         `yaml:"clusters"`
}

// Label is one resolved custom label. Values reach here already interpolated.
type Label struct {
	Key   string
	Value string
}

var envRef = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(:-[^}]*)?\}`)

var labelKeyRE = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// interpolate replaces every ${VAR} in s with its environment value, returning an
// error if any referenced variable is unset. Failing fast turns a typo'd secret
// name into a config-load error instead of repeated runtime auth failures.
//
// A reference may carry a fallback as ${VAR:-default}, borrowing the shell /
// docker-compose syntax and its meaning: unset OR empty falls back, and the reference
// never errors. That lets a shipped config.yaml drive a non-secret setting from the
// environment while still starting on a host that never exported it. Use it only where a
// safe default exists — a bare ${VAR} keeps the fail-loud behaviour that protects secrets.
func interpolate(s string) (string, error) {
	var missing []string
	out := envRef.ReplaceAllStringFunc(s, func(m string) string {
		sub := envRef.FindStringSubmatch(m)
		name, fallback := sub[1], sub[2]
		v, ok := os.LookupEnv(name)
		if ok && v != "" {
			return v
		}
		if fallback != "" {
			return fallback[len(":-"):] // group 2 keeps its ":-" prefix, so "" means absent
		}
		if !ok {
			missing = append(missing, name)
		}
		return ""
	})
	if len(missing) > 0 {
		return "", fmt.Errorf("unset environment variable(s): %s", strings.Join(missing, ", "))
	}
	return out, nil
}

// interpolateLabels resolves ${ENV} references in a label map in place. Keys are
// never interpolated: they are part of the metric schema, not a secret.
func interpolateLabels(labels map[string]string, what string) error {
	for k, v := range labels {
		iv, err := interpolate(v)
		if err != nil {
			return fmt.Errorf("%s label %s: %w", what, k, err)
		}
		labels[k] = iv
	}
	return nil
}

// validateLabels enforces the custom-label model: globally declared keys with
// non-empty values, and per-cluster overrides that may only restate a declared
// key. Rejecting an undeclared cluster key at load is what keeps the label-key
// set identical across clusters, as ADR-0006 requires.
func validateLabels(cfg *Config) error {
	for k, v := range cfg.Labels {
		if !labelKeyRE.MatchString(k) {
			return fmt.Errorf("label %q: key must match [a-zA-Z_][a-zA-Z0-9_]*", k)
		}
		if strings.HasPrefix(k, "__") {
			return fmt.Errorf("label %q: keys starting with __ are reserved by Prometheus", k)
		}
		if v == "" {
			return fmt.Errorf("label %q: value must not be empty", k)
		}
	}
	for _, c := range cfg.Clusters {
		for k, v := range c.Labels {
			if _, ok := cfg.Labels[k]; !ok {
				return fmt.Errorf("cluster %q: unknown label key %q (declare it in the top-level labels block)", c.Name, k)
			}
			if v == "" {
				return fmt.Errorf("cluster %q: label %q: value must not be empty", c.Name, k)
			}
		}
	}
	return nil
}

// EffectiveLabels returns one cluster's custom labels: the global block with
// that cluster's value overrides applied, sorted by key. Sorted because ADR-0006
// makes the ordered label-key set part of a metric's schema, so the order must
// not depend on YAML authoring order or Go map iteration order.
func (c Config) EffectiveLabels(cl Cluster) []Label {
	if len(c.Labels) == 0 {
		return nil
	}
	out := make([]Label, 0, len(c.Labels))
	for k, v := range c.Labels {
		if ov, ok := cl.Labels[k]; ok {
			v = ov
		}
		out = append(out, Label{Key: k, Value: v})
	}
	slices.SortFunc(out, func(a, b Label) int { return strings.Compare(a.Key, b.Key) })
	return out
}

// Load reads, interpolates ${ENV} references, applies defaults, and validates.
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if err := interpolateLabels(cfg.Labels, "global"); err != nil {
		return nil, err
	}
	for i := range cfg.Clusters {
		c := &cfg.Clusters[i]
		host, err := interpolate(c.Host)
		if err != nil {
			return nil, fmt.Errorf("cluster %s host: %w", c.Name, err)
		}
		c.Host = host
		username, err := interpolate(c.Username)
		if err != nil {
			return nil, fmt.Errorf("cluster %s username: %w", c.Name, err)
		}
		c.Username = username
		pw, err := interpolate(c.Password)
		if err != nil {
			return nil, fmt.Errorf("cluster %s password: %w", c.Name, err)
		}
		c.Password = pw
		if c.PasswordFile != "" && c.Password == "" {
			b, err := os.ReadFile(c.PasswordFile)
			if err != nil {
				return nil, fmt.Errorf("cluster %s passwordFile: %w", c.Name, err)
			}
			c.Password = strings.TrimSpace(string(b))
		}
		if err := c.InsecureSkipVerify.Resolve(interpolate); err != nil {
			return nil, fmt.Errorf("cluster %s insecureSkipVerify: %w", c.Name, err)
		}
		if c.MgmtPort == 0 {
			c.MgmtPort = 4443
		}
		if c.ObjPort == 0 {
			c.ObjPort = 9021
		}
		if c.DTPort == 0 {
			c.DTPort = 9101
		}
		if c.Name == "" {
			c.Name = c.Host
		}
		if err := interpolateLabels(c.Labels, "cluster "+c.Name); err != nil {
			return nil, err
		}
	}
	if cfg.Server.Port == "" {
		cfg.Server.Port = "9438"
	}
	if cfg.Server.URI == "" {
		cfg.Server.URI = "/metrics"
	}
	if cfg.Collection.Interval == 0 {
		cfg.Collection.Interval = 5 * time.Minute
	}
	if cfg.Collection.Timeout == 0 {
		cfg.Collection.Timeout = 60 * time.Second
	}
	if cfg.OTLP.Interval == 0 {
		cfg.OTLP.Interval = 10 * time.Second
	}
	if len(cfg.Clusters) == 0 {
		return nil, fmt.Errorf("no clusters configured")
	}
	if err := validateLabels(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
