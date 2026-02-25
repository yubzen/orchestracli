package rag

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildProjectBrief(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	mustWrite := func(rel, content string) {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	mustWrite("go.mod", "module example.com/test\n")
	mustWrite("README.md", "# Test\n")
	mustWrite("cmd/orchestra/main.go", "package main\n")
	mustWrite("internal/app/service.go", "package app\n")
	mustWrite("internal/app/service_test.go", "package app\n")

	brief, err := BuildProjectBrief(root)
	if err != nil {
		t.Fatalf("build brief: %v", err)
	}

	if !strings.Contains(brief, "Working directory:") {
		t.Fatalf("expected working directory in brief: %q", brief)
	}
	if !strings.Contains(brief, "Detected languages:") {
		t.Fatalf("expected language summary in brief: %q", brief)
	}
	if !strings.Contains(brief, "Go(") {
		t.Fatalf("expected Go language detection in brief: %q", brief)
	}
	if !strings.Contains(brief, "cmd/orchestra/main.go") {
		t.Fatalf("expected file tree preview in brief: %q", brief)
	}
}
