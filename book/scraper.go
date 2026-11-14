package book

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	urllib "net/url"
	"strings"
	"sync"
	"time"

	readability "codeberg.org/readeck/go-readability/v2"
	"github.com/PuerkitoBio/goquery"
	"github.com/chromedp/chromedp"

	"github.com/lapwat/papeer/book/store"
)

type ScrapeConfig struct {
	Depth       int
	Selector    string
	Quiet       bool
	Limit       int
	Offset      int
	Reverse     bool
	Delay       int
	Threads     int
	Include     bool
	ImagesOnly  bool
	UseLinkName bool
	PrintURL    bool
	Browser     bool
}

func NewScrapeConfig() *ScrapeConfig {
	return &ScrapeConfig{0, "", false, -1, 0, false, -1, -1, true, false, false, false, false}
}

func NewScrapeConfigQuiet() *ScrapeConfig {
	return &ScrapeConfig{0, "", true, -1, 0, false, -1, -1, true, false, false, false, false}
}

func NewScrapeConfigNoInclude() *ScrapeConfig {
	return &ScrapeConfig{0, "", false, -1, 0, false, -1, -1, false, false, false, false, false}
}

func NewScrapeConfigs(selectors []string) []*ScrapeConfig {
	configs := []*ScrapeConfig{}

	for _, s := range selectors {
		config := NewScrapeConfig()
		config.Selector = s

		configs = append(configs, config)
	}

	return configs
}

func NewScrapeConfigsAjin() []*ScrapeConfig {
	config0 := NewScrapeConfig()
	config0.Depth = 0
	config0.Selector = ".dt>a"
	config0.Limit = 3
	config0.Offset = 0
	config0.Delay = 5000
	config0.Include = false

	config1 := NewScrapeConfig()
	config1.Depth = 1
	config1.Selector = ".nav_apb>a"
	config1.Limit = 3
	config1.Offset = 1
	config1.Delay = 5000
	config1.Include = false

	config2 := NewScrapeConfig()
	config2.Depth = 2
	config2.ImagesOnly = true

	return []*ScrapeConfig{config0, config1, config2}
}

func NewScrapeConfigsWikipedia() []*ScrapeConfig {
	config0 := NewScrapeConfig()
	config0.Depth = 0
	config0.Threads = -1
	config0.Include = true

	config1 := NewScrapeConfig()
	config1.Depth = 1
	config1.Include = true

	return []*ScrapeConfig{config0, config1}
}

func NewScrapeConfigFake() *ScrapeConfig {
	config := NewScrapeConfig()
	config.Include = false

	return config
}

var (
	browserCtx  context.Context
	browserOnce sync.Once
	browserErr  error
)

func getBrowserContext() (context.Context, error) {
	browserOnce.Do(func() {
		allocCtx, _ := chromedp.NewExecAllocator(context.Background(),
			append(
				chromedp.DefaultExecAllocatorOptions[:],
				chromedp.NoSandbox,
				chromedp.UserAgent("papeer"),
			)...,
		)
		browserCtx, _ = chromedp.NewContext(allocCtx)
		_, browserErr = chromedp.Run(browserCtx, chromedp.Title())
	})
	return browserCtx, browserErr
}

// httpTimeout bounds every standard HTTP request so a hanging site cannot
// stall a whole book scrape.
const httpTimeout = 30 * time.Second

// fetchHTMLStd performs a GET request and returns the response body.
// Unlike the previous implementation it inspects the status code and
// returns an error on server/HTTP error responses instead of handing an
// error page to the content extractor.
func fetchHTMLStd(url string) (io.ReadCloser, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "papeer")

	client := &http.Client{Timeout: httpTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		resp.Body.Close()
		return nil, fmt.Errorf("unexpected HTTP status %d fetching %s", resp.StatusCode, url)
	}
	return resp.Body, nil
}

func fetchHTMLWithBrowser(url string) (io.ReadCloser, error) {
	ctx, err := getBrowserContext()
	if err != nil {
		return nil, fmt.Errorf("headless browser unavailable: %w", err)
	}
	ctx, cancel := chromedp.NewContext(ctx)
	defer cancel()

	ctx, cancel = context.WithTimeout(ctx, httpTimeout)
	defer cancel()

	var htmlContent string
	err = chromedp.Do(ctx,
		chromedp.Navigate(url),
		chromedp.WaitReady("body"),
		chromedp.Func(func(ctx context.Context, t *chromedp.Target) error {
			var err error
			htmlContent, err = chromedp.Run(ctx, chromedp.OuterHTML("html"))
			return err
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("headless browser: %w", err)
	}

	return io.NopCloser(strings.NewReader(htmlContent)), nil
}

// fetchHTML fetches a page using the configured transport and returns the
// full response body.
func fetchHTML(url string, useBrowser bool) ([]byte, error) {
	var response io.ReadCloser
	var err error
	if useBrowser {
		response, err = fetchHTMLWithBrowser(url)
	} else {
		response, err = fetchHTMLStd(url)
	}
	if err != nil {
		return nil, err
	}
	defer response.Close()

	return io.ReadAll(response)
}

// parseArticle runs readability over an already fetched HTML body.
func parseArticle(body []byte, baseURL *urllib.URL) (readability.Article, error) {
	article, err := readability.FromReader(bytes.NewReader(body), baseURL)
	if err != nil {
		return readability.Article{}, fmt.Errorf("failed to parse %s: %w", baseURL.String(), err)
	}
	return article, nil
}

// buildChapterContent renders the readability article to the chapter HTML
// content, handling lazy images, image-only mode and downloading/storing
// every referenced image through the supplied library.
func buildChapterContent(article *readability.Article, baseURL *urllib.URL, config *ScrapeConfig, bs *store.BookStore) (string, map[string]string, map[string][]byte, []string, error) {
	var buffer bytes.Buffer
	if err := article.RenderHTML(&buffer); err != nil {
		return "", nil, nil, nil, err
	}

	doc, err := goquery.NewDocumentFromReader(&buffer)
	if err != nil {
		return "", nil, nil, nil, err
	}

	// handle lazy images
	doc.Find("img").Each(func(i int, source *goquery.Selection) {
		src, exists := source.Attr("data-lazy-src")
		if exists {
			source.SetAttr("src", src)
		}
	})
	doc.Find("source").Remove()

	var content string
	if config.ImagesOnly {
		// append every image to content
		doc.Find("img").Each(func(i int, s *goquery.Selection) {
			imageTag, _ := goquery.OuterHtml(s)
			content += imageTag
		})
	} else {
		content, err = doc.Find("[id*=readability-page]").Html()
		if err != nil {
			return "", nil, nil, nil, err
		}
	}

	imageMap, imageData, missing, err := processImages(doc, baseURL, bs)
	if err != nil {
		return "", nil, nil, nil, err
	}
	return content, imageMap, imageData, missing, nil
}

// ScrapeOptions carries the library context of one scraping run.
type ScrapeOptions struct {
	// Store is the target library, nil for legacy in-memory scrapes.
	Store *store.BookStore
	// Refresh forces re-fetching pages already present in the library.
	Refresh bool
	// depth is the chapter tree depth of the current page (entry = 0).
	depth int
	// parent is the normalized URL of the parent page, empty for the entry.
	parent string
}

// NewChapterFromURL keeps the legacy signature used by the existing tests
// and callers that do not persist into a library. It performs an in-memory
// scrape and preserves the historical fail-fast behavior.
func NewChapterFromURL(url, linkName string, configs []*ScrapeConfig, index int, updateProgressBarName func(index int, name string)) chapter {
	c, err := scrapeChapter(url, linkName, configs, index, updateProgressBarName, nil)
	if err != nil {
		log.Fatal(err)
	}
	return c
}

// scrapeChapter scrapes one page and, when opts.Store is set, persists
// it and its images in the local library. Failures of individual pages are
// returned as errors instead of terminating the process, so callers can
// record them and keep scraping the rest of the book.
func scrapeChapter(url, linkName string, configs []*ScrapeConfig, index int, updateProgressBarName func(index int, name string), opts *ScrapeOptions) (chapter, error) {
	config := configs[0]

	baseURL, err := urllib.Parse(url)
	if err != nil {
		return chapter{}, err
	}

	normalized, err := store.NormalizeURL(url)
	if err != nil {
		return chapter{}, err
	}

	depth, parent := 0, ""
	if opts != nil {
		depth, parent = opts.depth, opts.parent
	}

	// offline reuse: a page already stored is read back from the library
	// without any network access
	var bs *store.BookStore
	if opts != nil {
		bs = opts.Store
	}
	if bs != nil && !opts.Refresh && bs.HasPage(normalized) {
		return chapterFromStore(normalized, url, linkName, configs, index, updateProgressBarName, opts)
	}

	// step 1: fetch the raw HTML over the network
	body, err := fetchHTML(url, config.Browser)
	if err != nil {
		if bs != nil {
			if recordErr := bs.RecordFailure(normalized, url, err.Error(), parent, depth, index); recordErr != nil {
				return chapter{}, recordErr
			}
			// on a refresh failure, keep using the last known good page
			if bs.HasPage(normalized) {
				return chapterFromStore(normalized, url, linkName, configs, index, updateProgressBarName, opts)
			}
		}
		return chapter{}, err
	}

	// step 2: extract article content and metadata with readability
	article, err := parseArticle(body, baseURL)
	if err != nil {
		if bs != nil {
			if recordErr := bs.RecordFailure(normalized, url, err.Error(), parent, depth, index); recordErr != nil {
				return chapter{}, recordErr
			}
			// on a refresh failure, keep using the last known good page
			if bs.HasPage(normalized) {
				return chapterFromStore(normalized, url, linkName, configs, index, updateProgressBarName, opts)
			}
		}
		return chapter{}, err
	}

	name := linkName
	if !config.UseLinkName {
		name = article.Title()
		updateProgressBarName(index, name)
	}

	// step 3: build chapter HTML and handle images
	content := ""
	var imageMap map[string]string
	var imageData map[string][]byte
	var missingImages []string
	if config.Include {
		content, imageMap, imageData, missingImages, err = buildChapterContent(&article, baseURL, config, bs)
		if err != nil {
			return chapter{}, err
		}
	}

	// step 4: discover and recurse into subchapters
	var subchapters []chapter
	var scrapeErr error
	if len(configs) > 1 {
		childOpts := childOptions(opts, normalized, depth)
		subchapters, scrapeErr = scrapeSubChapters(body, baseURL, configs, config, name, childOpts)
	}
	// when a library is attached, every subchapter failure has already been
	// recorded in its manifest: the run must continue and render the
	// successful pages instead of failing the whole book.
	if bs != nil {
		scrapeErr = nil
	}

	c := chapter{
		url:           url,
		body:          string(body),
		name:          name,
		author:        article.Byline(),
		content:       content,
		subChapters:   subchapters,
		config:        config,
		images:        imageMap,
		imageData:     imageData,
		missingImages: missingImages,
		depth:         depth,
		parent:        parent,
	}

	if bs != nil {
		if err := persistChapter(bs, normalized, url, body, c, index); err != nil {
			return chapter{}, err
		}
		c.storeRoot = bs.Dir()
	}

	return c, scrapeErr
}

// childOptions derives the options used for the subchapters of a page.
func childOptions(opts *ScrapeOptions, parentNormalized string, parentDepth int) *ScrapeOptions {
	if opts == nil {
		return nil
	}
	clone := *opts
	clone.depth = parentDepth + 1
	clone.parent = parentNormalized
	return &clone
}

// scrapeSubChapters discovers the links of a page and scrapes every linked
// page with the remaining config levels. A failing subchapter is recorded
// and skipped, it never aborts the whole book.
func scrapeSubChapters(body []byte, baseURL *urllib.URL, configs []*ScrapeConfig, config *ScrapeConfig, name string, opts *ScrapeOptions) ([]chapter, error) {
	links, _, err := discoverLinks(body, baseURL, config)
	if err != nil {
		return nil, err
	}

	var p progress
	if !config.Quiet {
		p = NewProgress(links, name, optsDepth(opts))
	}

	// preallocate so concurrent goroutines never append to the same slice;
	// failed positions stay zero-valued and are compacted afterwards
	results := make([]*chapter, len(links))
	var mu sync.Mutex
	var firstErr error
	rememberErr := func(err error) {
		mu.Lock()
		defer mu.Unlock()
		if firstErr == nil {
			firstErr = err
		}
	}

	appendChapter := func(index int, l link) {
		u, err := baseURL.Parse(l.Href)
		if err != nil {
			rememberErr(err)
			return
		}

		sc, err := scrapeChapter(u.String(), l.Text, configs[1:], index, p.UpdateName, opts)
		if err != nil {
			rememberErr(err)
			return
		}
		results[index] = &sc
		if !config.Quiet {
			p.Increment(index)
		}
	}

	if config.Delay >= 0 {
		// synchronous mode
		for index, link := range links {
			appendChapter(index, link)
			time.Sleep(time.Duration(config.Delay) * time.Millisecond)
		}
	} else {
		// asynchronous mode
		var wg sync.WaitGroup

		threads := config.Threads
		if threads == -1 {
			threads = len(links)
		}
		if threads < 1 {
			threads = 1
		}
		semaphore := make(chan struct{}, threads)

		for index, l := range links {
			wg.Add(1)
			semaphore <- struct{}{}

			go func(index int, l link) {
				defer wg.Done()
				appendChapter(index, l)
				<-semaphore
			}(index, l)
		}
		wg.Wait()
	}

	subchapters := make([]chapter, 0, len(links))
	for _, r := range results {
		if r != nil {
			subchapters = append(subchapters, *r)
		}
	}
	return subchapters, firstErr
}

func optsDepth(opts *ScrapeOptions) int {
	if opts == nil {
		return 0
	}
	return opts.depth
}

// discoverLinks extracts chapter links from a fetched page body, supporting
// both RSS/Atom feeds and HTML documents.
func discoverLinks(body []byte, baseURL *urllib.URL, config *ScrapeConfig) ([]link, string, error) {
	// try feed parsing first, fall back to HTML
	feedLinks, feedErr := extractFeedLinks(baseURL, string(body))
	if feedErr == nil && len(feedLinks) > 0 {
		links := feedLinks
		if config.Limit != -1 {
			end := config.Limit + config.Offset
			if end > len(links) {
				end = len(links)
			}
			if config.Offset > end {
				return nil, "", fmt.Errorf("offset %d beyond %d links", config.Offset, len(links))
			}
			links = links[config.Offset:end]
		}
		if config.Reverse {
			for i, j := 0, len(links)-1; i < j; i, j = i+1, j-1 {
				links[i], links[j] = links[j], links[i]
			}
		}
		return links, "RSS", nil
	}

	reader := *strings.NewReader(string(body))
	return extractHTMLLinks(baseURL, config.Selector, config.Limit, config.Offset, config.Reverse, reader)
}

// chapterFromStore rebuilds a chapter entirely from the local library.
func chapterFromStore(normalized, originalURL, linkName string, configs []*ScrapeConfig, index int, updateProgressBarName func(index int, name string), opts *ScrapeOptions) (chapter, error) {
	bs := opts.Store
	config := configs[0]

	data, record, err := bs.ReadPage(normalized)
	if err != nil {
		return chapter{}, err
	}

	baseURL, err := urllib.Parse(record.OriginalURL)
	if err != nil {
		return chapter{}, err
	}

	article, err := parseArticle(data, baseURL)
	if err != nil {
		return chapter{}, err
	}

	name := linkName
	if !config.UseLinkName {
		name = article.Title()
		updateProgressBarName(index, name)
	}

	content := ""
	var imageMap map[string]string
	var missingImages []string
	if config.Include {
		content, imageMap, _, missingImages, err = buildChapterContent(&article, baseURL, config, bs)
		if err != nil {
			return chapter{}, err
		}
	}

	var subchapters []chapter
	if len(configs) > 1 {
		subchapters, err = rebuildSubChaptersFromStore(record, configs, opts)
		if err != nil {
			return chapter{}, err
		}
	}

	return chapter{
		url:           record.OriginalURL,
		body:          string(data),
		name:          name,
		author:        article.Byline(),
		content:       content,
		subChapters:   subchapters,
		config:        config,
		storeRoot:     bs.Dir(),
		images:        imageMap,
		missingImages: missingImages,
		depth:         record.Depth,
		parent:        record.Parent,
	}, nil
}

// rebuildSubChaptersFromStore reconstructs the child chapters of a page by
// following the Parent links stored in the manifest. Children are emitted in
// manifest chapter order so the chapter sequence is stable across renders.
func rebuildSubChaptersFromStore(parent *store.PageRecord, configs []*ScrapeConfig, opts *ScrapeOptions) ([]chapter, error) {
	bs := opts.Store

	// collect children in manifest order
	children := make([]*store.PageRecord, 0)
	for _, u := range bs.ChapterURLs() {
		rec := bs.Page(u)
		if rec == nil || rec.Parent != parent.URL {
			continue
		}
		children = append(children, rec)
	}

	subchapters := make([]chapter, 0, len(children))
	for i, rec := range children {
		if rec.Status != store.StatusSuccess {
			continue
		}
		sc, err := chapterFromStore(rec.URL, rec.OriginalURL, rec.Title, configs[1:], i, func(int, string) {}, opts)
		if err != nil {
			return nil, err
		}
		subchapters = append(subchapters, sc)
	}
	return subchapters, nil
}

// persistChapter writes one fully fetched page and its images into the
// library and appends it to the manifest chapter order.
func persistChapter(bs *store.BookStore, normalized, originalURL string, body []byte, c chapter, order int) error {
	images := make([]store.ImageRecord, 0, len(c.images)+len(c.missingImages))

	// successfully downloaded or already stored images
	for src, rel := range c.images {
		images = append(images, store.ImageRecord{Source: src, Path: rel})
	}
	// images that failed to download
	for _, src := range c.missingImages {
		images = append(images, store.ImageRecord{Source: src, Missing: true})
	}

	rec := store.PageRecord{
		URL:         normalized,
		OriginalURL: originalURL,
		Title:       c.name,
		Depth:       c.depth,
		Parent:      c.parent,
		Order:       order,
		Images:      images,
		Summary:     contentSummary(string(body)),
	}
	return bs.SavePage(rec, body, c.imageData)
}

// contentSummary returns a short deterministic summary of page content,
// stored alongside the sha256 digest for human inspection.
func contentSummary(body string) string {
	trimmed := strings.TrimSpace(stripTags(body))
	if len(trimmed) > 200 {
		trimmed = trimmed[:200]
	}
	return trimmed
}

func stripTags(s string) string {
	var out strings.Builder
	inTag := false
	for _, r := range s {
		switch {
		case r == '<':
			inTag = true
		case r == '>':
			inTag = false
		case !inTag:
			out.WriteRune(r)
		}
	}
	return out.String()
}

func GetPath(elm *goquery.Selection) string {
	path := []string{}

	for {
		selector := strings.ToLower(goquery.NodeName(elm))
		if len(selector) == 0 {
			break
		}

		path = append(path, selector)
		elm = elm.Parent()
	}

	join := strings.Join(path, "<")
	return join
}

// GetLinks keeps the legacy signature used by the `list` command. It
// performs network access itself and returns errors instead of exiting.
func GetLinks(url *urllib.URL, selector string, limit, offset int, reverse, include, Browser bool) ([]link, string, chapter, error) {
	body, err := fetchHTML(url.String(), Browser)
	if err != nil {
		return []link{}, "", chapter{}, err
	}

	config := NewScrapeConfig()
	config.Selector = selector
	config.Limit = limit
	config.Offset = offset
	config.Reverse = reverse
	config.Browser = Browser

	links, pathMax, err := discoverLinks(body, url, config)
	if err != nil {
		return []link{}, "", chapter{}, err
	}

	home, err := scrapeChapter(url.String(), "", []*ScrapeConfig{NewScrapeConfig()}, 0, func(index int, name string) {}, nil)
	if err != nil {
		return []link{}, "", chapter{}, err
	}

	// include home page
	if include {
		l := NewLink(url.String(), home.Name(), &time.Time{})
		links = append([]link{l}, links...)
	}

	return links, pathMax, home, nil
}
