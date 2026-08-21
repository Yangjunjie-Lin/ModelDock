package secrets

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestFileManagerReadsRotatedSecretWithoutLoggingValue(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "JWT_SECRET")
	if err := os.WriteFile(path, []byte("first-secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := NewFileManager(root)
	value, err := manager.Get(context.Background(), "JWT_SECRET")
	if err != nil || value != "first-secret" {
		t.Fatalf("first secret=%q err=%v", value, err)
	}
	if err := os.WriteFile(path, []byte("rotated-secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	value, err = manager.Get(context.Background(), "JWT_SECRET")
	if err != nil || value != "rotated-secret" {
		t.Fatalf("rotated secret=%q err=%v", value, err)
	}
}

func TestFileManagerRejectsPathTraversal(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "secrets")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parent, "outside"), []byte("must-not-read"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := NewFileManager(root)
	if value, err := manager.Get(context.Background(), "../outside"); !errors.Is(err, ErrNotFound) || value != "" {
		t.Fatalf("path traversal returned value=%q err=%v", value, err)
	}
}
