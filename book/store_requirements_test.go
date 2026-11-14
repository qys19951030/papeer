package book

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lapwat/papeer/book/store"
)

// Same page reached via a fragment or a trailing slash must collapse to a
// single stored page record.
func TestReqURLVariantsDedup(t *testing.T) {
	srv, _ := fakeSite(t, nil)
	lib, _ := NewTestLibrary(t, t.TempDir(), srv.URL+"/")
	found, _ := lib.FindBook(srv.URL + "/")

	variants := []string{
		srv.URL + "/chapter1#frag",
		srv.URL + "/chapter1/",
	}
	seen := map[string]bool{}
	for _, v := range variants {
		n, err := store.NormalizeURL(v)
		if err != nil {
			t.Fatal(err)
		}
		seen[n] = true
	}
	if len(seen) != 1 {
		t.Fatalf("variants did not collapse: %v", seen)
	}
	count := 0
	for u := range found.Manifest.Pages {
		if strings.HasSuffix(u, "/chapter1") {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected 1 chapter1 record, got %d", count)
	}
}

// A truncated page file must make offline rendering fail, name the page and
// wrap ErrCorruptPage.
func TestReqTruncationNamesPage(t *testing.T) {
	srv, _ := fakeSite(t, nil)
	lib, _ := NewTestLibrary(t, t.TempDir(), srv.URL+"/")
	found, _ := lib.FindBook(srv.URL + "/")
	rec := found.Page(srv.URL + "/chapter2")
	truncateTestFile(t, found, rec.Path)

	_, err := ChapterFromStore(found, srv.URL+"/", false, false)
	if err == nil {
		t.Fatal("expected corruption error")
	}
	if !errors.Is(err, store.ErrCorruptPage) || !strings.Contains(err.Error(), "/chapter2") {
		t.Fatalf("error must name chapter2 and wrap ErrCorruptPage: %v", err)
	}
}

// A missing image (404) is counted on the page but must not block the
// chapter or the rest of the book.
func TestReqMissingImageDoesNotBlock(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			fmt.Fprint(w, `<!doctype html><html><head><title>Broken Img Book</title></head>
<body><nav><a class="toc" href="/c1">One</a></nav><p>intro</p></body></html>`)
		case "/c1":
			fmt.Fprint(w, `<!doctype html><html><head><title>C1</title></head>
<body><article><h1>C1</h1><p>text</p><img src="/missing.png"/></article></body></html>`)
		case "/missing.png":
			http.NotFound(w, r)
		default:
			http.NotFound(w, r)
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	lib, _ := NewTestLibrary(t, t.TempDir(), srv.URL+"/")
	found, _ := lib.FindBook(srv.URL + "/")

	rec := found.Page(srv.URL + "/c1")
	if rec == nil {
		t.Fatal("chapter record missing")
	}
	if rec.MissingImages != 1 || rec.ImageCount != 1 {
		t.Fatalf("expected 1 missing of 1 image, got count=%d missing=%d", rec.ImageCount, rec.MissingImages)
	}
	if !rec.Images[0].Missing {
		t.Fatal("image record should be marked missing")
	}

	// rendering still works despite the missing image
	c, err := ChapterFromStore(found, srv.URL+"/", false, false)
	if err != nil {
		t.Fatalf("missing image must not block render: %v", err)
	}
	if len(c.SubChapters()) != 1 {
		t.Fatalf("chapter dropped despite recoverable missing image: %d", len(c.SubChapters()))
	}
}
