package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// fakeSiteHandler is reimplemented here because the cmd package cannot use
// the book package's in-package test helpers.
func writeFakeSite(t *testing.T, fail *atomic.Bool) (string, func()) {
	t.Helper()

	mux := newFakeMux(fail)
	srv := startTestServer(mux)
	return srv.URL, srv.Close
}

func TestGetStoreAndFromStore(t *testing.T) {
	var fail atomic.Bool
	url, closeFn := writeFakeSite(t, &fail)
	defer closeFn()

	storeDir := filepath.Join(t.TempDir(), "papeer-store")
	workDir := t.TempDir()

	// first run: scrape and store
	cmd := exec.Command(testBinary(t),
		"get", url+"/",
		"--store", storeDir,
		"--selector", "a.toc",
		"--depth", "1",
		"--format", "md",
		"--quiet",
	)
	cmd.Dir = workDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("first get failed: %v\n%s", err, out)
	}

	// the library must contain a committed book with a manifest
	entries, err := os.ReadDir(storeDir)
	if err != nil {
		t.Fatalf("store not created: %v", err)
	}
	var bookDir string
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			bookDir = e.Name()
		}
	}
	if bookDir == "" {
		t.Fatal("no committed book directory found in store")
	}
	if _, err := os.Stat(filepath.Join(storeDir, bookDir, "manifest.json")); err != nil {
		t.Fatalf("manifest missing: %v", err)
	}

	// stop the server to prove --from-store performs no network access
	closeFn()

	render := exec.Command(testBinary(t),
		"get", url+"/",
		"--from-store",
		"--store", storeDir,
		"--format", "md",
		"--quiet",
	)
	render.Dir = workDir
	rOut, rErr := render.CombinedOutput()
	if rErr != nil {
		t.Fatalf("--from-store render failed offline: %v\n%s", rErr, rOut)
	}
	if !strings.Contains(string(rOut), "Chapter One") && !fileContainsMarkdown(workDir) {
		t.Errorf("offline render produced no chapter content: %s", rOut)
	}

	// locating an unknown book must fail
	missing := exec.Command(testBinary(t),
		"get", "http://127.0.0.1:1/nope/",
		"--from-store", "--store", storeDir, "--quiet",
	)
	if err := missing.Run(); err == nil {
		t.Error("expected error when book is not in store")
	}
}

func TestGetFailureExitCode(t *testing.T) {
	var fail atomic.Bool
	fail.Store(true)
	url, closeFn := writeFakeSite(t, &fail)
	defer closeFn()

	storeDir := filepath.Join(t.TempDir(), "papeer-store")
	workDir := t.TempDir()

	cmd := exec.Command(testBinary(t),
		"get", url+"/",
		"--store", storeDir,
		"--selector", "a.toc",
		"--depth", "1",
		"--format", "md",
		"--quiet",
	)
	cmd.Dir = workDir
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected non-zero exit on failed page, output: %s", out)
	}
	if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() != 1 {
		t.Errorf("expected exit code 1, got %d", exitErr.ExitCode())
	}
	if !strings.Contains(string(out), "chapter2") {
		t.Errorf("failure output should name the failed page: %s", out)
	}
}

func fileContainsMarkdown(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".md") {
			data, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err == nil && strings.Contains(string(data), "Chapter One") {
				return true
			}
		}
	}
	return false
}
