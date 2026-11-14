package book

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/lapwat/papeer/book/store"
)

// TestOfflineStoreScrapeAndRender covers:
//  1. first scrape stores pages and images in the library
//  2. offline rendering (--from-store equivalent) produces md/html without
//     any network access, and two renders are byte-identical
func TestOfflineStoreScrapeAndRender(t *testing.T) {
	srv, h := fakeSite(t, nil)
	libRoot := t.TempDir()

	lib, err := store.New(libRoot)
	if err != nil {
		t.Fatal(err)
	}
	bs, err := lib.CreateBook("Fake Book", "Test Author", srv.URL+"/")
	if err != nil {
		t.Fatal(err)
	}

	c, failures, err := ScrapeToStore(bs, srv.URL+"/", tocConfigs(), false)
	if err != nil {
		t.Fatalf("scrape: %v", err)
	}
	if len(failures) != 0 {
		t.Fatalf("unexpected failures: %v", failures)
	}
	if err := bs.Commit(); err != nil {
		t.Fatal(err)
	}

	// manifest assertions on real fields
	found, err := lib.FindBook(srv.URL + "/")
	if err != nil {
		t.Fatalf("FindBook: %v", err)
	}
	m := found.Manifest
	if m.Name != "Fake Book" || m.Author != "Test Author" {
		t.Errorf("book metadata wrong: %+v", m)
	}
	if len(m.Chapters) != 3 {
		t.Fatalf("expected 3 chapters (entry + 2), got %d: %v", len(m.Chapters), m.Chapters)
	}
	for _, u := range m.Chapters {
		rec := m.Pages[u]
		if rec == nil || rec.Status != store.StatusSuccess {
			t.Fatalf("page %s not stored successfully: %+v", u, rec)
		}
		if rec.Bytes <= 0 || rec.Digest == "" {
			t.Errorf("page %s missing bytes/digest: %+v", u, rec)
		}
		if rec.FetchedAt == "" || rec.Summary == "" {
			t.Errorf("page %s missing fetched_at/summary: %+v", u, rec)
		}
	}

	// image assertions
	ch1 := m.Pages[srv.URL+"/chapter1"]
	if ch1.ImageCount != 2 || ch1.MissingImages != 0 {
		t.Errorf("chapter1 image count wrong: %+v", ch1)
	}
	for _, img := range ch1.Images {
		if img.Missing {
			t.Errorf("image unexpectedly missing: %+v", img)
		}
		if _, ok := found.ImageAbsPath(img.Path); !ok {
			t.Errorf("image file not on disk: %+v", img)
		}
	}
	ch2 := m.Pages[srv.URL+"/chapter2"]
	if ch2.ImageCount != 1 || ch2.MissingImages != 0 {
		t.Errorf("chapter2 image count wrong: %+v", ch2)
	}

	// the scraped tree contains both subchapters in order
	subs := c.SubChapters()
	if len(subs) != 2 {
		t.Fatalf("expected 2 subchapters, got %d", len(subs))
	}
	if subs[0].Name() != "Chapter One" || subs[1].Name() != "Chapter Two" {
		t.Errorf("subchapter order/names wrong: %q, %q", subs[0].Name(), subs[1].Name())
	}

	// offline rendering with the server shut down must still work
	srv.Close()

	offline, err := ChapterFromStore(found, srv.URL+"/", false, false)
	if err != nil {
		t.Fatalf("offline render: %v", err)
	}

	md1 := ToMarkdownString(offline)
	html1 := ToHtmlString(offline)
	if !strings.Contains(md1, "Chapter One") || !strings.Contains(md1, "Chapter Two") {
		t.Error("markdown missing chapter content")
	}
	if !strings.Contains(html1, "Chapter One") || !strings.Contains(html1, "Chapter Two") {
		t.Error("html missing chapter content")
	}

	// second render must be byte-identical (same library -> same output)
	offline2, err := ChapterFromStore(found, srv.URL+"/", false, false)
	if err != nil {
		t.Fatalf("second offline render: %v", err)
	}
	md2 := ToMarkdownString(offline2)
	html2 := ToHtmlString(offline2)
	if md1 != md2 {
		t.Error("markdown render not deterministic across runs")
	}
	if html1 != html2 {
		t.Error("html render not deterministic across runs")
	}

	// chapter order identical between the two renders
	var order1, order2 []string
	for _, sc := range offline.SubChapters() {
		order1 = append(order1, sc.Name())
	}
	for _, sc := range offline2.SubChapters() {
		order2 = append(order2, sc.Name())
	}
	if strings.Join(order1, ",") != strings.Join(order2, ",") {
		t.Errorf("chapter order differs: %v vs %v", order1, order2)
	}

	// md image references point at library-hosted absolute http sources that
	// are backed by stored files (no temp files, no dependency on the site)
	if !strings.Contains(md1, "/img1.png") {
		t.Error("markdown does not reference stored image")
	}

	// request counts: each page requested exactly once during scraping
	for path, want := range map[string]int32{
		"/":         1,
		"/chapter1": 1,
		"/chapter2": 1,
	} {
		if got := h.count(path); got != want {
			t.Errorf("path %s requested %d times, want %d", path, got, want)
		}
	}
}

// TestStoreReuseNoDuplicateRequests asserts that a second run reuses stored
// pages and images without hitting the server again.
func TestStoreReuseNoDuplicateRequests(t *testing.T) {
	srv, h := fakeSite(t, nil)
	libRoot := t.TempDir()

	lib, _ := store.New(libRoot)
	bs, _ := lib.CreateBook("Fake Book", "", srv.URL+"/")
	if _, _, err := ScrapeToStore(bs, srv.URL+"/", tocConfigs(), false); err != nil {
		t.Fatal(err)
	}
	if err := bs.Commit(); err != nil {
		t.Fatal(err)
	}

	found, err := lib.FindBook(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}

	// second run: everything is in the library
	c, failures, err := ScrapeToStore(found, srv.URL+"/", tocConfigs(), false)
	if err != nil {
		t.Fatalf("second scrape: %v", err)
	}
	if len(failures) != 0 {
		t.Fatalf("unexpected failures on reuse: %v", failures)
	}
	if len(c.SubChapters()) != 2 {
		t.Fatalf("reused tree missing subchapters: %d", len(c.SubChapters()))
	}

	for path, want := range map[string]int32{
		"/":         1,
		"/chapter1": 1,
		"/chapter2": 1,
		"/img1.png": 1,
		"/img2.png": 1,
		"/img3.png": 1,
	} {
		if got := h.count(path); got != want {
			t.Errorf("path %s requested %d times across two runs, want %d", path, got, want)
		}
	}
}

// TestStoreSinglePageErrorRecordedNotFatal verifies that a server error on
// one chapter only records a failure; the rest of the book is still stored
// and rendered.
func TestStoreSinglePageErrorRecordedNotFatal(t *testing.T) {
	var fail atomic.Bool
	fail.Store(true)
	srv, _ := fakeSite(t, &fail)
	libRoot := t.TempDir()

	lib, _ := store.New(libRoot)
	bs, _ := lib.CreateBook("Fake Book", "", srv.URL+"/")

	c, failures, err := ScrapeToStore(bs, srv.URL+"/", tocConfigs(), false)
	if err != nil {
		t.Fatalf("a chapter failure must not fail the whole scrape: %v", err)
	}
	if err := bs.Commit(); err != nil {
		t.Fatal(err)
	}

	if len(failures) == 0 {
		t.Fatal("expected at least one recorded failure")
	}
	found, _ := lib.FindBook(srv.URL + "/")
	m := found.Manifest

	if len(m.Failures) == 0 {
		t.Fatal("manifest has no failure entries")
	}
	failedURL := srv.URL + "/chapter2"
	var sawFailure bool
	for _, f := range m.Failures {
		if f.URL == failedURL && f.Error != "" {
			sawFailure = true
		}
	}
	if !sawFailure {
		t.Errorf("chapter2 failure not recorded with reason: %+v", m.Failures)
	}

	// the failed page must not have a backing file
	rec := m.Pages[failedURL]
	if rec == nil || rec.Status != store.StatusFailed {
		t.Fatalf("failed page record wrong: %+v", rec)
	}
	if rec.Path != "" {
		t.Errorf("failed page must not reference a file, got %q", rec.Path)
	}

	// successful pages are still present and renderable
	good := m.Pages[srv.URL+"/chapter1"]
	if good == nil || good.Status != store.StatusSuccess {
		t.Fatalf("successful chapter1 missing: %+v", good)
	}

	// the rendered tree still contains the good chapter
	if len(c.SubChapters()) != 1 {
		t.Fatalf("expected only the good chapter in tree, got %d", len(c.SubChapters()))
	}
	if c.SubChapters()[0].Name() != "Chapter One" {
		t.Errorf("wrong surviving chapter: %q", c.SubChapters()[0].Name())
	}

	// failure list is exposed for the CLI exit code
	if len(found.FailureURLs()) == 0 {
		t.Error("FailureURLs empty despite a failed page")
	}
}

// TestStoreRefreshUpdatesContent verifies --refresh re-fetches pages and
// the stored fetched_at/digest follow the new content while reused pages
// stay untouched.
func TestStoreRefreshUpdatesContent(t *testing.T) {
	var fail atomic.Bool
	srv, h := fakeSite(t, &fail)
	libRoot := t.TempDir()

	lib, _ := store.New(libRoot)
	bs, _ := lib.CreateBook("Fake Book", "", srv.URL+"/")
	if _, _, err := ScrapeToStore(bs, srv.URL+"/", tocConfigs(), false); err != nil {
		t.Fatal(err)
	}
	if err := bs.Commit(); err != nil {
		t.Fatal(err)
	}

	found, _ := lib.FindBook(srv.URL + "/")
	beforeDigest := found.Manifest.Pages[srv.URL+"/chapter1"].Digest
	beforeTime := found.Manifest.Pages[srv.URL+"/chapter1"].FetchedAt

	// refresh with chapter2 failing: chapter1 is re-requested and its
	// fetched_at updates; chapter2 keeps its last known good page
	fail.Store(true)
	if _, _, err := ScrapeToStore(found, srv.URL+"/", tocConfigs(), true); err != nil {
		t.Fatalf("refresh scrape: %v", err)
	}
	if got := h.count("/chapter1"); got != 2 {
		t.Errorf("refreshed chapter1 requested %d times, want 2", got)
	}
	after := found.Manifest.Pages[srv.URL+"/chapter1"]
	if after.FetchedAt == beforeTime {
		t.Error("fetched_at did not update after --refresh")
	}
	if after.Digest != beforeDigest {
		t.Error("identical content should keep the same digest on refresh")
	}
	rec2 := found.Manifest.Pages[srv.URL+"/chapter2"]
	if rec2 == nil || rec2.Status != store.StatusSuccess || rec2.Path == "" {
		t.Errorf("refresh failure should retain last known good page: %+v", rec2)
	}
	if len(found.Manifest.Failures) == 0 {
		t.Error("refresh failure not recorded")
	}

	// serve changed content and confirm digest changes on the next refresh
	fail.Store(false)
	h.variant.Store(true)
	if _, _, err := ScrapeToStore(found, srv.URL+"/", tocConfigs(), true); err != nil {
		t.Fatalf("content-change refresh: %v", err)
	}
	changed := found.Manifest.Pages[srv.URL+"/chapter1"]
	if changed.Digest == beforeDigest {
		t.Error("digest did not change after the page content changed")
	}
	if changed.Summary == "" {
		t.Error("summary not refreshed")
	}
}

// TestStoreCorruptPageFailsRender asserts that a truncated page file or a
// digest mismatch makes offline rendering fail naming the page.
func TestStoreCorruptPageFailsRender(t *testing.T) {
	srv, _ := fakeSite(t, nil)
	libRoot := t.TempDir()

	lib, _ := store.New(libRoot)
	bs, _ := lib.CreateBook("Fake Book", "", srv.URL+"/")
	if _, _, err := ScrapeToStore(bs, srv.URL+"/", tocConfigs(), false); err != nil {
		t.Fatal(err)
	}
	if err := bs.Commit(); err != nil {
		t.Fatal(err)
	}
	srv.Close()

	found, _ := lib.FindBook(srv.URL + "/")
	rec := found.Manifest.Pages[srv.URL+"/chapter1"]
	pagePath := filepath.Join(found.Dir(), filepath.FromSlash(rec.Path))

	// truncate the file
	original, err := os.ReadFile(pagePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pagePath, original[:len(original)/2], 0o644); err != nil {
		t.Fatal(err)
	}

	_, err = ChapterFromStore(found, srv.URL+"/", false, false)
	if err == nil {
		t.Fatal("expected error rendering a truncated library")
	}
	if !strings.Contains(err.Error(), "/chapter1") {
		t.Errorf("error should name the corrupt page, got: %v", err)
	}
	if !errors.Is(err, store.ErrCorruptPage) {
		t.Errorf("expected ErrCorruptPage, got: %v", err)
	}

	// restore size but change content -> digest mismatch
	corrupt := append([]byte{}, original...)
	corrupt[len(corrupt)-1] ^= 0xFF
	if err := os.WriteFile(pagePath, corrupt, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = ChapterFromStore(found, srv.URL+"/", false, false)
	if err == nil || !errors.Is(err, store.ErrCorruptPage) {
		t.Fatalf("expected digest mismatch ErrCorruptPage, got: %v", err)
	}
}
