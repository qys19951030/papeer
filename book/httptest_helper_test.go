package book

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/lapwat/papeer/book/store"
)

// fakeSite builds an httptest server with:
//   - /          a table-of-contents page linking to two chapters
//   - /chapter1  a chapter with two images
//   - /chapter2  a chapter with one image
//   - /img1.png, /img2.png, /img3.png small PNG-ish payloads
//
// requestCount counts hits per path so tests can assert that cached pages
// are not requested twice. failPath makes /chapter2 return 500 while set,
// and variant flips chapter1 to serve changed content.
func fakeSite(t *testing.T, failPath *atomic.Bool) (*httptest.Server, siteHandles) {
	t.Helper()

	var variant atomic.Bool

	// pre-seeded counters plus a mutex: the server handles requests
	// concurrently, so map writes must be synchronized
	var counterMu sync.Mutex
	counts := map[string]*int32{}
	count := func(path string) *int32 {
		counterMu.Lock()
		defer counterMu.Unlock()
		c, ok := counts[path]
		if !ok {
			c = new(int32)
			counts[path] = c
		}
		return c
	}

	png := func(n byte) []byte {
		// a deterministic non-empty payload; content does not need to be a
		// valid PNG for the scraper/store tests
		return []byte{0x89, 'P', 'N', 'G', n, n, n}
	}

	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(count(r.URL.Path), 1)
		if failPath != nil && failPath.Load() && r.URL.Path == "/chapter2" {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		switch r.URL.Path {
		case "/", "/index.html":
			fmt.Fprint(w, `<!doctype html><html><head><title>Fake Book</title></head>
<body>
<h1>Fake Book</h1>
<nav><a class="toc" href="/chapter1">Chapter One</a>
<a class="toc" href="/chapter2">Chapter Two</a></nav>
<p>Intro paragraph of the fake book.</p>
</body></html>`)
		case "/chapter1":
			extra := ""
			if variant.Load() {
				extra = "<p>Refreshed extra paragraph that changes the digest.</p>"
			}
			fmt.Fprintf(w, `<!doctype html><html><head><title>Chapter One</title></head>
<body><article>
<h1>Chapter One</h1>
<p>First chapter body text.</p>
%s
<p><img src="/img1.png" alt="one"/></p>
<p><img src="/img2.png" alt="two"/></p>
</article></body></html>`, extra)
		case "/chapter2":
			fmt.Fprint(w, `<!doctype html><html><head><title>Chapter Two</title></head>
<body><article>
<h1>Chapter Two</h1>
<p>Second chapter body text.</p>
<p><img src="/img3.png" alt="three"/></p>
</article></body></html>`)
		case "/img1.png":
			w.Header().Set("Content-Type", "image/png")
			w.Write(png(1))
		case "/img2.png":
			w.Header().Set("Content-Type", "image/png")
			w.Write(png(2))
		case "/img3.png":
			w.Header().Set("Content-Type", "image/png")
			w.Write(png(3))
		default:
			http.NotFound(w, r)
		}
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, siteHandles{variant: &variant, counts: counts, mu: &counterMu}
}

// siteHandles exposes the knobs and request counters of a fake site.
type siteHandles struct {
	variant *atomic.Bool
	counts  map[string]*int32
	mu      *sync.Mutex
}

func (h siteHandles) count(path string) int32 {
	counterMu := h.mu
	counterMu.Lock()
	defer counterMu.Unlock()
	if c := h.counts[path]; c != nil {
		return atomic.LoadInt32(c)
	}
	return 0
}

// tocConfigs builds the two-level scraper configs used by the fake site.
func tocConfigs() []*ScrapeConfig {
	root := NewScrapeConfigQuiet()
	root.Selector = "a.toc"
	root.Include = true

	child := NewScrapeConfigQuiet()
	child.Include = true
	return []*ScrapeConfig{root, child}
}

func loadTestManifest(t *testing.T, dir string) *store.Manifest {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, store.ManifestName))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	m := &store.Manifest{}
	if err := json.Unmarshal(data, m); err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	return m
}

// NewTestLibrary runs one full scrape of entryURL into a fresh library and
// commits it, returning the open store.
func NewTestLibrary(t *testing.T, libRoot, entryURL string) (*store.Store, *store.BookStore) {
	t.Helper()
	lib, err := store.New(libRoot)
	if err != nil {
		t.Fatal(err)
	}
	bs, err := lib.CreateBook("Fake Book", "Test Author", entryURL)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := ScrapeToStore(bs, entryURL, tocConfigs(), false); err != nil {
		t.Fatalf("initial scrape: %v", err)
	}
	if err := bs.Commit(); err != nil {
		t.Fatal(err)
	}
	return lib, bs
}
func truncateTestFile(t *testing.T, bs interface{ Dir() string }, rel string) {
	t.Helper()
	path := bs.Dir() + "/" + rel
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data[:len(data)/2], 0o644); err != nil {
		t.Fatal(err)
	}
}
