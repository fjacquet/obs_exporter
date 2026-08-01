package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
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
	if c.CollectFlux {
		t.Error("Flux collection should default to disabled")
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
	// Unset collectQuotas must not be dragged along by collectMetering: false.
	if !cfg.Clusters[0].QuotasEnabled() {
		t.Error("quotas should default to enabled when collectQuotas is unset")
	}
}

func TestLoadQuotasDisable(t *testing.T) {
	p := write(t, `
clusters:
  - name: ecs1
    host: ecs1.example.com
    username: monitor
    password: x
    collectQuotas: false
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Clusters[0].QuotasEnabled() {
		t.Error("quotas should be disabled")
	}
	// Quotas are a knob inside metering, not a replacement for it: the bulk
	// billing call must still run.
	if !cfg.Clusters[0].MeteringEnabled() {
		t.Error("metering should stay enabled")
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

func TestLoadLabels(t *testing.T) {
	t.Setenv("TEAM_NAME", "storage-ops")
	p := write(t, `
labels:
  site: geneva
  env: prod
  owner: ${TEAM_NAME}
clusters:
  - name: ecs1
    host: ecs1.example.com
    labels:
      site: zurich
  - name: ecs2
    host: ecs2.example.com
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}

	got := cfg.EffectiveLabels(cfg.Clusters[0])
	want := []Label{{"env", "prod"}, {"owner", "storage-ops"}, {"site", "zurich"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("EffectiveLabels(ecs1) = %v, want %v", got, want)
	}

	got = cfg.EffectiveLabels(cfg.Clusters[1])
	want = []Label{{"env", "prod"}, {"owner", "storage-ops"}, {"site", "geneva"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("EffectiveLabels(ecs2) = %v, want %v", got, want)
	}
}

func TestEffectiveLabelsEmptyWithoutGlobalBlock(t *testing.T) {
	p := write(t, `
clusters:
  - name: ecs1
    host: ecs1.example.com
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.EffectiveLabels(cfg.Clusters[0]); got != nil {
		t.Errorf("EffectiveLabels = %v, want nil", got)
	}
}

func TestLoadLabelsRejected(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		want string
	}{
		{
			name: "invalid key",
			yaml: "labels:\n  \"my-site\": geneva\n",
			want: "must match",
		},
		{
			name: "reserved prefix",
			yaml: "labels:\n  __site: geneva\n",
			want: "reserved",
		},
		{
			name: "empty global value",
			yaml: "labels:\n  site: \"\"\n",
			want: "must not be empty",
		},
		{
			name: "undeclared cluster key",
			yaml: "labels:\n  site: geneva\n",
			want: "unknown label key",
		},
		{
			name: "cluster labels without global block",
			yaml: "",
			want: "unknown label key",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cluster := "clusters:\n  - name: ecs1\n    host: ecs1.example.com\n"
			switch tc.name {
			case "undeclared cluster key", "cluster labels without global block":
				cluster += "    labels:\n      rack: r12\n"
			}
			_, err := Load(write(t, tc.yaml+cluster))
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to contain %q", err, tc.want)
			}
		})
	}
}

func TestLoadLabelsEmptyClusterValue(t *testing.T) {
	p := write(t, `
labels:
  site: geneva
clusters:
  - name: ecs1
    host: ecs1.example.com
    labels:
      site: ""
`)
	if _, err := Load(p); err == nil || !strings.Contains(err.Error(), "must not be empty") {
		t.Fatalf("err = %v, want an empty-value error", err)
	}
}

func TestLoadLabelsUnsetEnvVar(t *testing.T) {
	p := write(t, `
labels:
  owner: ${OBS_LABELS_UNSET_VAR}
clusters:
  - name: ecs1
    host: ecs1.example.com
`)
	if _, err := Load(p); err == nil || !strings.Contains(err.Error(), "unset environment variable") {
		t.Fatalf("err = %v, want an unset-variable error", err)
	}
}

func TestLoadClusterLabelsUnsetEnvVarNamesDefaultedCluster(t *testing.T) {
	p := write(t, `
clusters:
  - host: ecs1.example.com
    labels:
      owner: ${OBS_LABELS_UNSET_VAR}
`)
	_, err := Load(p)
	if err == nil || !strings.Contains(err.Error(), "unset environment variable") {
		t.Fatalf("err = %v, want an unset-variable error", err)
	}
	if !strings.Contains(err.Error(), "cluster ecs1.example.com") {
		t.Fatalf("err = %v, want it to name the host-defaulted cluster (ecs1.example.com), not an empty name", err)
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
