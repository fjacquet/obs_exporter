package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func write(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadDefaults(t *testing.T) {
	p := write(t, `
clusters:
  - name: ecs1
    host: ecs1.example.com
    username: monitor
    password: secret
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Port != "9438" || cfg.Server.URI != "/metrics" {
		t.Errorf("server defaults wrong: %+v", cfg.Server)
	}
	if cfg.Collection.Interval != 5*time.Minute || cfg.Collection.Timeout != 60*time.Second {
		t.Errorf("collection defaults wrong: %+v", cfg.Collection)
	}
	c := cfg.Clusters[0]
	if c.MgmtPort != 4443 || c.ObjPort != 9021 || c.DTPort != 9101 {
		t.Errorf("port defaults wrong: %+v", c)
	}
	if c.BaseURL() != "https://ecs1.example.com:4443" {
		t.Errorf("BaseURL = %s", c.BaseURL())
	}
	if !c.MeteringEnabled() {
		t.Error("metering should default to enabled")
	}
	if c.CollectDT {
		t.Error("DT collection should default to disabled")
	}
}

func TestLoadEnvInterpolation(t *testing.T) {
	t.Setenv("ECS1_PASSWORD", "s3cr3t")
	p := write(t, `
clusters:
  - name: ecs1
    host: ecs1.example.com
    username: monitor
    password: "${ECS1_PASSWORD}"
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Clusters[0].Password != "s3cr3t" {
		t.Errorf("password = %q", cfg.Clusters[0].Password)
	}
}

func TestLoadEnvInterpolationHostUsername(t *testing.T) {
	t.Setenv("ECS1_HOST", "ecs01.prod.example.com")
	t.Setenv("ECS1_USERNAME", "ecs-monitor")
	t.Setenv("ECS1_PASSWORD", "s3cr3t")
	p := write(t, `
clusters:
  - name: ecs1
    host: "${ECS1_HOST}"
    username: "${ECS1_USERNAME}"
    password: "${ECS1_PASSWORD}"
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	c := cfg.Clusters[0]
	if c.Host != "ecs01.prod.example.com" {
		t.Errorf("host = %q", c.Host)
	}
	if c.Username != "ecs-monitor" {
		t.Errorf("username = %q", c.Username)
	}
	if c.Password != "s3cr3t" {
		t.Errorf("password = %q", c.Password)
	}
}

func TestLoadMissingEnvFails(t *testing.T) {
	p := write(t, `
clusters:
  - name: ecs1
    host: ecs1.example.com
    username: monitor
    password: "${DEFINITELY_NOT_SET_12345}"
`)
	if _, err := Load(p); err == nil {
		t.Fatal("expected error for unset env var")
	}
}

func TestLoadMissingEnvHostFails(t *testing.T) {
	p := write(t, `
clusters:
  - name: ecs1
    host: "${DEFINITELY_NOT_SET_HOST_12345}"
    username: monitor
    password: secret
`)
	if _, err := Load(p); err == nil {
		t.Fatal("expected error for unset host env var")
	}
}

func TestLoadMissingEnvUsernameFails(t *testing.T) {
	p := write(t, `
clusters:
  - name: ecs1
    host: ecs1.example.com
    username: "${DEFINITELY_NOT_SET_USER_12345}"
    password: secret
`)
	if _, err := Load(p); err == nil {
		t.Fatal("expected error for unset username env var")
	}
}

func TestLoadPasswordFile(t *testing.T) {
	pwFile := filepath.Join(t.TempDir(), "pw")
	if err := os.WriteFile(pwFile, []byte("filepw\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := write(t, `
clusters:
  - name: ecs1
    host: ecs1.example.com
    username: monitor
    passwordFile: `+pwFile+`
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Clusters[0].Password != "filepw" {
		t.Errorf("password = %q", cfg.Clusters[0].Password)
	}
}

func TestLoadNoClustersFails(t *testing.T) {
	p := write(t, `server: {port: "9438"}`)
	if _, err := Load(p); err == nil {
		t.Fatal("expected error for empty cluster list")
	}
}

func TestLoadMeteringDisable(t *testing.T) {
	p := write(t, `
clusters:
  - name: ecs1
    host: ecs1.example.com
    username: monitor
    password: x
    collectMetering: false
    collectDT: true
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Clusters[0].MeteringEnabled() {
		t.Error("metering should be disabled")
	}
	if !cfg.Clusters[0].CollectDT {
		t.Error("DT should be enabled")
	}
}

func TestLoadInsecureSkipVerifyNativeBool(t *testing.T) {
	p := write(t, `
clusters:
  - name: ecs1
    host: ecs1.example.com
    username: monitor
    password: x
    insecureSkipVerify: true
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Clusters[0].InsecureSkipVerify.Bool() {
		t.Error("insecureSkipVerify should be true")
	}
}

func TestLoadInsecureSkipVerifyDefaultFalse(t *testing.T) {
	p := write(t, `
clusters:
  - name: ecs1
    host: ecs1.example.com
    username: monitor
    password: x
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Clusters[0].InsecureSkipVerify.Bool() {
		t.Error("insecureSkipVerify should default to false")
	}
}

func TestLoadInsecureSkipVerifyEnvRefTrue(t *testing.T) {
	t.Setenv("OBS1_SKIP_CERTIFICATE", "true")
	p := write(t, `
clusters:
  - name: ecs1
    host: ecs1.example.com
    username: monitor
    password: x
    insecureSkipVerify: "${OBS1_SKIP_CERTIFICATE}"
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Clusters[0].InsecureSkipVerify.Bool() {
		t.Error("insecureSkipVerify should resolve to true from env")
	}
}

func TestLoadInsecureSkipVerifyEnvRefFalse(t *testing.T) {
	t.Setenv("OBS1_SKIP_CERTIFICATE", "false")
	p := write(t, `
clusters:
  - name: ecs1
    host: ecs1.example.com
    username: monitor
    password: x
    insecureSkipVerify: "${OBS1_SKIP_CERTIFICATE}"
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Clusters[0].InsecureSkipVerify.Bool() {
		t.Error("insecureSkipVerify should resolve to false from env")
	}
}

func TestLoadInsecureSkipVerifyMissingEnvFails(t *testing.T) {
	p := write(t, `
clusters:
  - name: ecs1
    host: ecs1.example.com
    username: monitor
    password: x
    insecureSkipVerify: "${DEFINITELY_NOT_SET_SKIP_CERT_12345}"
`)
	if _, err := Load(p); err == nil {
		t.Fatal("expected error for unset insecureSkipVerify env var")
	}
}

func TestLoadInsecureSkipVerifyNonBoolEnvFails(t *testing.T) {
	t.Setenv("OBS1_SKIP_CERTIFICATE", "not-a-bool")
	p := write(t, `
clusters:
  - name: ecs1
    host: ecs1.example.com
    username: monitor
    password: x
    insecureSkipVerify: "${OBS1_SKIP_CERTIFICATE}"
`)
	if _, err := Load(p); err == nil {
		t.Fatal("expected error for non-boolean insecureSkipVerify env value")
	}
}

func TestWatcherTrigger(t *testing.T) {
	p := write(t, `
clusters:
  - name: ecs1
    host: ecs1.example.com
    username: monitor
    password: x
`)
	w, err := NewWatcher(p)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()
	w.Trigger()
	select {
	case cfg := <-w.Updates():
		if cfg.Clusters[0].Name != "ecs1" {
			t.Errorf("unexpected config: %+v", cfg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no update received")
	}
}
