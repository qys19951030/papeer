package book

import (
	"log"
	urllib "net/url"

	"github.com/lapwat/papeer/book/store"
)

// logFatal preserves the historical fail-fast behavior of the formatting
// entry points (file creation etc.) while keeping log usage in one place.
func logFatal(err error) {
	log.Fatal(err)
}

// normalizeImageSrc mirrors the image URL normalization used while
// scraping, so the epub renderer can look an image up in the chapter's
// stored image map.
func normalizeImageSrc(src string) (string, error) {
	u, err := urllib.Parse(src)
	if err != nil {
		return "", err
	}
	return store.NormalizeImageURL(u.String())
}
