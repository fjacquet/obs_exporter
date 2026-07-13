// Package config loads and validates the exporter configuration.
package config

import (
	"fmt"
	"os"
	"regexp"
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
	// CollectMetering enables the namespace quota/billing collector (default true).
	// Billing can be slow on large clusters; disable it (or raise
	// collection.interval) if needed. Pointer so "unset" defaults to enabled.
	CollectMetering *bool `yaml:"collectMetering"`
}

// MeteringEnabled reports whether the metering collector should run.
func (c Cluster) MeteringEnabled() bool {
	return c.CollectMetering == nil || *c.CollectMetering
}

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
	Clusters   []Cluster  `yaml:"clusters"`
}

var envRef = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// interpolate replaces every ${VAR} in s with its environment value, returning an
// error if any referenced variable is unset. Failing fast turns a typo'd secret
// name into a config-load error instead of repeated runtime auth failures.
func interpolate(s string) (string, error) {
	var missing []string
	out := envRef.ReplaceAllStringFunc(s, func(m string) string {
		name := envRef.FindStringSubmatch(m)[1]
		v, ok := os.LookupEnv(name)
		if !ok {
			missing = append(missing, name)
		}
		return v
	})
	if len(missing) > 0 {
		return "", fmt.Errorf("unset environment variable(s): %s", strings.Join(missing, ", "))
	}
	return out, nil
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
	return &cfg, nil
}
