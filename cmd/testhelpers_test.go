package cmd

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

// testBinary builds the papeer binary once and returns its path so the cmd
// tests can exercise the real CLI (flags, exit codes) in a subprocess.
var (
	testBinOnce sync.Once
	testBinPath string
	testBinErr  error
)

func testBinary(t *testing.T) string {
	t.Helper()
	testBinOnce.Do(func() {
		// build under GOCACHE (never mounted noexec) rather than /tmp
		base, err := os.UserCacheDir()
		if err != nil {
			testBinErr = err
			return
		}
		dir, err := os.MkdirTemp(base, "papeer-testbin-")
		if err != nil {
			testBinErr = err
			return
		}
		testBinPath = dir + "/papeer"
		testBinErr = buildPapeer(testBinPath)
	})
	if testBinErr != nil {
		t.Fatalf("build test binary: %v", testBinErr)
	}
	// some sandboxes strip the exec bit, re-assert it on every use
	if err := os.Chmod(testBinPath, 0o755); err != nil {
		t.Fatalf("chmod test binary: %v", err)
	}
	return testBinPath
}

func buildPapeer(path string) error {
	// tests run with package dir cmd/ as cwd; build the repository-root main
	// package explicitly or `go build .` would produce a cmd package archive
	root, err := repoRoot()
	if err != nil {
		return err
	}
	cmd := exec.Command("go", "build", "-o", path, root)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}
	// some sandboxes strip the exec bit from go build output
	return os.Chmod(path, 0o755)
}

// repoRoot resolves the module root from the test working directory.
func repoRoot() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return filepath.Dir(cwd), nil
}

func newFakeMux(fail *atomic.Bool) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if fail != nil && fail.Load() && r.URL.Path == "/chapter2" {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		switch r.URL.Path {
		case "/", "/index.html":
			fmt.Fprint(w, `<!doctype html><html><head><title>Fake Book</title></head>
<body><h1>Fake Book</h1>
<nav><a class="toc" href="/chapter1">Chapter One</a>
<a class="toc" href="/chapter2">Chapter Two</a></nav>
<p>Intro</p></body></html>`)
		case "/chapter1":
			fmt.Fprint(w, `<!doctype html><html><head><title>Chapter One</title></head>
<body><article><h1>Chapter One</h1><p>Body one</p>
<p><img src="/img1.png"/></p></article></body></html>`)
		case "/chapter2":
			fmt.Fprint(w, `<!doctype html><html><head><title>Chapter Two</title></head>
<body><article><h1>Chapter Two</h1><p>Body two</p></article></body></html>`)
		case "/img1.png":
			w.Header().Set("Content-Type", "image/png")
			w.Write([]byte{0x89, 'P', 'N', 'G', 1})
		default:
			http.NotFound(w, r)
		}
	})
	return mux
}

func startTestServer(mux *http.ServeMux) *httptest.Server {
	return httptest.NewServer(mux)
}
