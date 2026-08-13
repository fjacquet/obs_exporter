package config

import "testing"

// TestExpandFallback pins the ${VAR:-default} form ported from pscale_exporter: it falls
// back when the variable is unset OR exported empty (shell / docker-compose semantics),
// prefers a real value, and never errors — while a bare ${VAR} must keep failing loudly,
// which is what stops a missing secret from resolving to an empty string.
func TestExpandFallback(t *testing.T) {
	t.Setenv("OBS_FALLBACK_TEST_SET", "real")
	t.Setenv("OBS_FALLBACK_TEST_EMPTY", "")
	for _, tc := range []struct{ name, in, want string }{
		{"unset falls back", "${OBS_FALLBACK_TEST_UNSET:-false}", "false"},
		{"set wins over default", "${OBS_FALLBACK_TEST_SET:-false}", "real"},
		{"exported empty falls back", "${OBS_FALLBACK_TEST_EMPTY:-other}", "other"},
		{"empty default allowed", "${OBS_FALLBACK_TEST_UNSET:-}", ""},
		{"mixed with literal text", "a${OBS_FALLBACK_TEST_UNSET:-b}c", "abc"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := interpolate(tc.in)
			if err != nil {
				t.Fatalf("interpolate(%q): unexpected error: %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("interpolate(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
	if _, err := interpolate("${OBS_FALLBACK_TEST_UNSET}"); err == nil {
		t.Error("a bare reference to an unset variable must still fail")
	}
}
