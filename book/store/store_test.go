package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizeURL(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"fragment", "https://example.com/page#section", "https://example.com/page"},
		{"trailing slash", "https://example.com/page/", "https://example.com/page"},
		{"multiple trailing slashes", "https://example.com/page///", "https://example.com/page"},
		{"host case", "https://EXAMPLE.com/Page", "https://example.com/Page"},
		{"default http port", "http://example.com:80/page", "http://example.com/page"},
		{"default https port", "https://example.com:443/page", "https://example.com/page"},
		{"non default port kept", "http://example.com:8080/page", "http://example.com:8080/page"},
		{"root path kept", "https://example.com/", "https://example.com/"},
		{"query preserved", "https://example.com/page?id=1", "https://example.com/page?id=1"},
	}

	seen := map[string]bool{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NormalizeURL(tc.in)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("NormalizeURL(%q) = %q, want %q", tc.in, got, tc.want)
			}
			seen[got] = true
		})
	}
}

func TestNormalizeURLCollisions(t *testing.T) {
	variants := []string{
		"https://Example.COM:443/Path/",
		"https://example.com/Path#frag",
		"https://example.com/Path",
	}
	first, err := NormalizeURL(variants[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range variants[1:] {
		got, err := NormalizeURL(v)
		if err != nil {
			t.Fatal(err)
		}
		if got != first {
			t.Errorf("%q normalized to %q, want %q", v, got, first)
		}
	}
}

func TestBookLifecycle(t *testing.T) {
	root := t.TempDir()
	s, err := New(root)
	if err != nil {
		t.Fatal(err)
	}

	entry := "https://example.com/book/"
	if _, err := s.FindBook(entry); err == nil {
		t.Fatal("expected ErrBookNotFound on empty store")
	}

	b, err := s.CreateBook("My Book", "An Author", entry)
	if err != nil {
		t.Fatal(err)
	}

	html := []byte("<html><body>hello</body></html>")
	images := map[string][]byte{
		"https://example.com/img/a.png": []byte("png-bytes"),
	}
	rec := PageRecord{
		URL:         "https://example.com/book",
		OriginalURL: entry,
		Title:       "Chapter One",
		Depth:       0,
		Order:       0,
		Images:      []ImageRecord{{Source: "https://example.com/img/a.png"}},
		Summary:     "hello",
	}
	if err := b.RegisterChapterOrder(rec.URL); err != nil {
		t.Fatal(err)
	}
	if err := b.SavePage(rec, html, images); err != nil {
		t.Fatal(err)
	}

	// staging directory must not be discoverable before commit
	if _, err := s.FindBook(entry); err == nil {
		t.Fatal("staging book should not be discoverable before Commit")
	}

	if err := b.Commit(); err != nil {
		t.Fatal(err)
	}

	found, err := s.FindBook("https://example.com/book#frag")
	if err != nil {
		t.Fatalf("FindBook after commit: %v", err)
	}
	if found.Manifest.Name != "My Book" || found.Manifest.Author != "An Author" {
		t.Errorf("manifest metadata mismatch: %+v", found.Manifest)
	}
	if len(found.Manifest.Chapters) != 1 || found.Manifest.Chapters[0] != "https://example.com/book" {
		t.Errorf("chapter order wrong: %+v", found.Manifest.Chapters)
	}

	page := found.Manifest.Pages["https://example.com/book"]
	if page == nil || page.Status != StatusSuccess {
		t.Fatalf("page record missing or not successful: %+v", page)
	}
	if page.Bytes != int64(len(html)) || page.Digest == "" {
		t.Errorf("bytes/digest not recorded: %+v", page)
	}
	if page.ImageCount != 1 || len(page.Images) != 1 || page.Images[0].Missing {
		t.Errorf("image record wrong: %+v", page.Images)
	}
	if _, ok := found.ImageAbsPath(page.Images[0].Path); !ok {
		t.Error("stored image file missing")
	}

	// reading back verifies the digest
	data, _, err := found.ReadPage("https://example.com/book")
	if err != nil {
		t.Fatalf("ReadPage: %v", err)
	}
	if string(data) != string(html) {
		t.Error("read-back content mismatch")
	}
}

func TestVerifyPageDetectsTruncation(t *testing.T) {
	root := t.TempDir()
	s, _ := New(root)
	b, _ := s.CreateBook("B", "", "https://example.com/")
	html := []byte("<html>content</html>")
	rec := PageRecord{URL: "https://example.com", OriginalURL: "https://example.com/"}
	if err := b.SavePage(rec, html, nil); err != nil {
		t.Fatal(err)
	}
	if err := b.Commit(); err != nil {
		t.Fatal(err)
	}

	found, err := s.FindBook("https://example.com/")
	if err != nil {
		t.Fatal(err)
	}
	page := found.Manifest.Pages["https://example.com"]

	// truncate the backing file
	path := filepath.Join(found.Dir(), filepath.FromSlash(page.Path))
	if err := os.WriteFile(path, html[:4], 0o644); err != nil {
		t.Fatal(err)
	}
	if err := found.VerifyPage(page); err == nil {
		t.Error("expected integrity error on truncated page")
	}

	// restore size but corrupt the content
	if err := os.WriteFile(path, []byte("<html>CONTENT</html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := found.VerifyPage(page); err == nil {
		t.Error("expected digest mismatch on modified page")
	}
}

func TestRecordFailureLeavesNoOrphanFile(t *testing.T) {
	root := t.TempDir()
	s, _ := New(root)
	b, _ := s.CreateBook("B", "", "https://example.com/")

	if err := b.RecordFailure("https://example.com/missing", "https://example.com/missing/", "connection refused", "https://example.com", 1, 0); err != nil {
		t.Fatal(err)
	}
	if err := b.Commit(); err != nil {
		t.Fatal(err)
	}

	found, _ := s.FindBook("https://example.com/")
	if len(found.Manifest.Failures) != 1 {
		t.Fatalf("expected one failure, got %+v", found.Manifest.Failures)
	}
	page := found.Manifest.Pages["https://example.com/missing"]
	if page == nil || page.Status != StatusFailed || page.Path != "" {
		t.Errorf("failed page record wrong: %+v", page)
	}
	if entries, err := os.ReadDir(filepath.Join(found.Dir(), PageDir)); err != nil || len(entries) != 0 {
		t.Errorf("failed page must leave no file, got %v %v", entries, err)
	}
}
