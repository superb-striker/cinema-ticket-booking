package redis

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRedisTLSRejectsInvalidCA(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(path, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := redisTLSConfig(Config{TLSEnabled: true, TLSCAFile: path})
	if err == nil || !strings.Contains(err.Error(), "no valid certificates") {
		t.Fatalf("expected invalid CA error, got %v", err)
	}
}

func TestRedisTLSDisabledIgnoresTLSFiles(t *testing.T) {
	config, err := redisTLSConfig(Config{TLSCAFile: "/does/not/exist"})
	if err != nil || config != nil {
		t.Fatalf("expected no TLS configuration, got %#v, %v", config, err)
	}
}
