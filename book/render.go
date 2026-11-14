package book

import (
	"fmt"

	"github.com/lapwat/papeer/book/store"
)

// ChapterFromStore rebuilds the entry chapter (and its whole subtree) from
// a local library without any network access. Every stored page is
// integrity-checked before use, so a truncated or tampered file produces an
// error naming the offending page instead of a partially rendered book.
//
// The returned chapter tree uses the same config flags as a normal scrape
// (last level included, PrintURL/ImagesOnly propagated), which guarantees
// that rendering the same library twice yields identical text and chapter
// order.
func ChapterFromStore(bs *store.BookStore, entryURL string, printURL, imagesOnly bool) (chapter, error) {
	normalized, err := store.NormalizeURL(entryURL)
	if err != nil {
		return chapter{}, err
	}

	record := bs.Page(normalized)
	if record == nil {
		return chapter{}, fmt.Errorf("%w: entry page %s", store.ErrBookNotFound, entryURL)
	}

	// verify every stored page up front so the error can name any broken page
	for _, u := range bs.ChapterURLs() {
		rec := bs.Page(u)
		if rec == nil || rec.Status != store.StatusSuccess {
			continue
		}
		if err := bs.VerifyPage(rec); err != nil {
			return chapter{}, err
		}
	}

	// every stored page has content in the library, so every tree level
	// includes its text. Display flags are propagated from the run options.
	maxDepth := 0
	for _, rec := range bs.AllPages() {
		if rec.Depth > maxDepth {
			maxDepth = rec.Depth
		}
	}
	configs := make([]*ScrapeConfig, 0, maxDepth+1)
	for d := 0; d <= maxDepth; d++ {
		cfg := NewScrapeConfigQuiet()
		cfg.Include = true
		cfg.PrintURL = printURL
		cfg.ImagesOnly = imagesOnly
		configs = append(configs, cfg)
	}

	opts := &ScrapeOptions{Store: bs}
	return chapterFromStore(normalized, record.OriginalURL, record.Title,
		configs, 0, func(int, string) {}, opts)
}

// ScrapeToStore performs a full scraping run against a local library.
// Already stored pages are reused unless refresh is set. It returns the
// scraped entry chapter (zero value when the entry page itself failed), the
// normalized URLs of pages that failed during this run and the entry error.
// Failed pages are recorded in the library and never interrupt the rest of
// the book.
func ScrapeToStore(bs *store.BookStore, entryURL string, configs []*ScrapeConfig, refresh bool) (chapter, []string, error) {
	if err := bs.ClearFailures(); err != nil {
		return chapter{}, nil, err
	}

	opts := &ScrapeOptions{Store: bs, Refresh: refresh}
	c, err := scrapeChapter(entryURL, "", configs, 0, func(int, string) {}, opts)
	return c, bs.FailureURLs(), err
}
