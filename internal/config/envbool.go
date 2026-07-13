package config

import (
	"fmt"
	"strconv"
	"strings"
)

// EnvBool is a boolean config value that may be written in YAML either as a
// native boolean (insecureSkipVerify: true) or as a ${VAR} environment
// reference resolved at secret-resolution time (insecureSkipVerify:
// ${OBS1_SKIP_CERTIFICATE}). Backward compatible with existing native-bool
// configs; an omitted field defaults to false.
type EnvBool struct {
	raw string // ${...} reference (or literal string form), when written as a string
	val bool   // resolved value
}

// Bool returns the resolved boolean value.
func (b EnvBool) Bool() bool { return b.val }

// UnmarshalYAML accepts either a native YAML boolean or a string (which may
// be a ${VAR} reference resolved later by Resolve).
func (b *EnvBool) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var bv bool
	if err := unmarshal(&bv); err == nil {
		b.val = bv
		return nil
	}
	var s string
	if err := unmarshal(&s); err != nil {
		return fmt.Errorf("insecureSkipVerify must be a boolean or ${ENV} reference: %w", err)
	}
	b.raw = s
	return nil
}

// Resolve expands any ${VAR} reference (via expand) and parses the result as
// a boolean. It is a no-op when the value was a native boolean or omitted.
// An empty expansion resolves to false; a non-boolean expansion is an error.
func (b *EnvBool) Resolve(expand func(string) (string, error)) error {
	if b.raw == "" {
		return nil
	}
	s, err := expand(b.raw)
	if err != nil {
		return err
	}
	s = strings.TrimSpace(s)
	if s == "" {
		b.val = false
		return nil
	}
	v, err := strconv.ParseBool(s)
	if err != nil {
		return fmt.Errorf("insecureSkipVerify: cannot parse %q as boolean", s)
	}
	b.val = v
	return nil
}
