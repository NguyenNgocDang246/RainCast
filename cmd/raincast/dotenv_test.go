package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDotEnv(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	os.WriteFile(path, []byte("# comment\n\nRC_A=plain\nexport RC_B=\"quoted value\"\nRC_C='single'\nRC_KEEP=fromfile\n"), 0o600)
	t.Setenv("RC_KEEP", "fromenv")
	for _, k := range []string{"RC_A", "RC_B", "RC_C"} {
		os.Unsetenv(k)
		t.Cleanup(func() { os.Unsetenv(k) })
	}

	if err := loadDotEnv(path); err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]string{"RC_A": "plain", "RC_B": "quoted value", "RC_C": "single", "RC_KEEP": "fromenv"} {
		if got := os.Getenv(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	if err := loadDotEnv(filepath.Join(t.TempDir(), "missing")); err != nil {
		t.Errorf("missing file: %v", err)
	}
	os.WriteFile(path, []byte("oops\n"), 0o600)
	if err := loadDotEnv(path); err == nil {
		t.Error("malformed line should fail")
	}
}
