package db

import (
	"os"
	"path/filepath"
	"testing"
)

// TestFindDotEnvWalksUpToRepoRoot checks the search that lets both the
// server (started at the repository root) and `go test` (run from a deep
// package directory) find the same .env file — and that it stops at the
// repository boundary instead of picking up an unrelated parent file.
func TestFindDotEnvWalksUpToRepoRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("DATABASE_URL=\n"), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	// A repository marker at the root ends the search.
	if err := os.WriteFile(filepath.Join(root, "go.work"), []byte("go 1.26.0\n"), 0o600); err != nil {
		t.Fatalf("write go.work: %v", err)
	}
	deep := filepath.Join(root, "apps", "simulator", "internal", "db")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	restore := chdir(t, deep)
	defer restore()

	path, ok := findDotEnv()
	if !ok {
		t.Fatal("findDotEnv did not walk up to the repository .env")
	}
	if want := filepath.Join(root, ".env"); path != want {
		t.Fatalf("findDotEnv = %q, want %q", path, want)
	}
}

// TestFindDotEnvStopsAtRepositoryRoot proves the search does not escape the
// repository: a .env OUTSIDE the root must not be loaded.
func TestFindDotEnvStopsAtRepositoryRoot(t *testing.T) {
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, ".env"), []byte("DATABASE_URL=nope\n"), 0o600); err != nil {
		t.Fatalf("write outer .env: %v", err)
	}
	root := filepath.Join(outside, "repo")
	deep := filepath.Join(root, "apps", "simulator")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.work"), []byte("go 1.26.0\n"), 0o600); err != nil {
		t.Fatalf("write go.work: %v", err)
	}

	restore := chdir(t, deep)
	defer restore()

	if path, ok := findDotEnv(); ok {
		t.Fatalf("findDotEnv escaped the repository and found %q", path)
	}
}

// chdir moves into dir and returns a restore function.
func chdir(t *testing.T, dir string) func() {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir %s: %v", dir, err)
	}
	return func() {
		if err := os.Chdir(prev); err != nil {
			t.Fatalf("restore chdir: %v", err)
		}
	}
}
