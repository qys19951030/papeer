package book

import (
	"fmt"
	"io"
	"net/http"
	urllib "net/url"
	"path/filepath"
	"strings"

	"github.com/PuerkitoBio/goquery"

	"github.com/lapwat/papeer/book/store"
)

// imageClient is shared by image downloads and bounded by the same timeout
// as page fetches.
var imageClient = &http.Client{Timeout: httpTimeout}

// resolveImageURL turns an image src into an absolute URL and then into its
// normalized form. Data URLs and unparseable sources are returned as-is.
func resolveImageURL(src string, baseURL *urllib.URL) (absolute, normalized string, ok bool) {
	src = strings.TrimSpace(src)
	if src == "" || strings.HasPrefix(src, "data:") {
		return src, src, false
	}

	ref, err := urllib.Parse(src)
	if err != nil {
		return src, src, false
	}
	absoluteURL := baseURL.ResolveReference(ref)
	absolute = absoluteURL.String()

	normalized, err = store.NormalizeImageURL(absolute)
	if err != nil {
		normalized = absolute
	}
	return absolute, normalized, true
}

// storeRelativeImagePath mirrors the naming used by store.SavePage so the
// scraper and the library agree on image locations.
func storeRelativeImagePath(normalized string) string {
	return filepath.ToSlash(filepath.Join(store.ImageDir, store.ImageFileName(normalized)))
}

// imageFetch is the outcome of processing one referenced image.
type imageFetch struct {
	// source is the normalized absolute source URL.
	source string
	// path is the store-relative destination path.
	path string
	// data holds freshly downloaded bytes; nil when the image already
	// exists in the library or the download failed.
	data []byte
	// missing marks a failed download.
	missing bool
}

// processImages walks the images of an article document. When a library is
// available every http(s) image is resolved and, if not already present,
// downloaded; the bytes are returned for the store to write atomically.
// Images that fail to download are returned as missing and never interrupt
// the chapter. The goquery document is rewritten so the chapter content
// references the normalized absolute image source consistently.
func processImages(doc *goquery.Document, baseURL *urllib.URL, bs *store.BookStore) (map[string]string, map[string][]byte, []string, error) {
	imageMap := map[string]string{}
	imageData := map[string][]byte{}
	var missing []string

	var loopErr error
	doc.Find("img").Each(func(i int, s *goquery.Selection) {
		if loopErr != nil {
			return
		}
		src, exists := s.Attr("src")
		if !exists {
			return
		}
		// remove query part, matching the historical behavior
		src = strings.Split(src, "?")[0]

		absolute, normalized, downloadable := resolveImageURL(src, baseURL)
		if !downloadable {
			return
		}

		// always reference the normalized absolute source in content so an
		// offline rebuild produces byte-identical text
		s.SetAttr("src", absolute)

		if bs == nil {
			// legacy in-memory mode: nothing to store, rendering downloads
			// images on demand
			return
		}

		rel := storeRelativeImagePath(normalized)

		if _, existsOnDisk := bs.ImageAbsPath(rel); existsOnDisk {
			// reuse the library copy, no network access
			imageMap[normalized] = rel
			return
		}

		data, err := downloadImage(absolute)
		if err != nil {
			// record as missing but do NOT add it to imageMap, otherwise the
			// same image would be persisted once as present and once as
			// missing
			missing = append(missing, normalized)
			return
		}
		imageMap[normalized] = rel
		imageData[normalized] = data
	})

	if loopErr != nil {
		return nil, nil, nil, loopErr
	}
	return imageMap, imageData, missing, nil
}

// downloadImage fetches one image over HTTP with the standard client.
func downloadImage(url string) ([]byte, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "papeer")

	resp, err := imageClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return nil, fmt.Errorf("unexpected HTTP status %d fetching image %s", resp.StatusCode, url)
	}
	return io.ReadAll(resp.Body)
}
