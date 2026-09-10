package config

import (
	"os"
	"testing"
)

func TestEnvFileRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("VECTOR_CONFIG_DIR", dir)

	if err := SetEnv("OPENROUTER_API_KEY", "sk-or-secret"); err != nil {
		t.Fatalf("set: %v", err)
	}
	if err := SetEnv("OTHER_KEY", "value with spaces"); err != nil {
		t.Fatalf("set: %v", err)
	}
	m, err := ReadEnv()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if m["OPENROUTER_API_KEY"] != "sk-or-secret" {
		t.Fatalf("got %q", m["OPENROUTER_API_KEY"])
	}
	if m["OTHER_KEY"] != "value with spaces" {
		t.Fatalf("quoted value round-trip failed: %q", m["OTHER_KEY"])
	}

	info, err := os.Stat(EnvPath())
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("env file mode = %o, want 600", info.Mode().Perm())
	}

	if err := UnsetEnv("OPENROUTER_API_KEY"); err != nil {
		t.Fatalf("unset: %v", err)
	}
	m, _ = ReadEnv()
	if _, ok := m["OPENROUTER_API_KEY"]; ok {
		t.Fatal("key was not unset")
	}
}
