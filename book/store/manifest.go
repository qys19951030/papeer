package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// ManifestName is the name of the manifest file inside a book directory.
const ManifestName = "manifest.json"

// PageStatus values stored in PageRecord.Status.
const (
	// StatusSuccess marks a page whose HTML and metadata are fully stored.
	StatusSuccess = "success"
	// StatusFailed marks a page that could not be fetched or parsed.
	StatusFailed = "failed"
)

// ImageRecord describes one image referenced by a stored page.
type ImageRecord struct {
	// Source is the original, absolute image URL.
	Source string `json:"source"`
	// Path is the image location relative to the book directory.
	Path string `json:"path"`
	// Missing is true when the image could not be downloaded.
	Missing bool `json:"missing"`
}

// PageRecord is the per-page entry of the manifest.
type PageRecord struct {
	// URL is the normalized URL used as the unique page identifier.
	URL string `json:"url"`
	// OriginalURL is the URL exactly as encountered while scraping.
	OriginalURL string `json:"original_url"`
	// Title is the readability article title.
	Title string `json:"title"`
	// Status is either StatusSuccess or StatusFailed.
	Status string `json:"status"`
	// Path is the HTML file location relative to the book directory
	// (empty for failed pages).
	Path string `json:"path"`
	// Bytes is the size of the stored HTML file in bytes.
	Bytes int64 `json:"bytes"`
	// Digest is the sha256 hex digest of the stored HTML file. It is
	// used as the content summary and to detect truncated/corrupted files.
	Digest string `json:"digest"`
	// Summary is a short human-readable content summary.
	Summary string `json:"summary"`
	// FetchedAt is the last successful fetch time in RFC3339 format.
	FetchedAt string `json:"fetched_at"`
	// Depth is the chapter tree depth at which the page was visited,
	// used to rebuild the chapter tree offline.
	Depth int `json:"depth"`
	// Order is the visit order, used to keep the chapter sequence stable.
	Order int `json:"order"`
	// ImageCount is the number of images referenced by the page.
	ImageCount int `json:"image_count"`
	// MissingImages counts images that could not be downloaded.
	MissingImages int `json:"missing_images"`
	// Images lists the image records.
	Images []ImageRecord `json:"images"`
	// Error holds the failure reason for failed pages.
	Error string `json:"error,omitempty"`
}

// FailureRecord is an entry of the manifest failure list.
type FailureRecord struct {
	URL          string `json:"url"`
	OriginalURL  string `json:"original_url"`
	Error        string `json:"error"`
	FailedAt     string `json:"failed_at"`
}

// Manifest is the JSON document describing one stored book.
type Manifest struct {
	// Name is the book title.
	Name string `json:"name"`
	// Author is the book author, empty when unknown.
	Author string `json:"author"`
	// Entry is the normalized URL of the entry point given on the CLI.
	Entry string `json:"entry"`
	// EntryOriginal is the entry point URL exactly as given on the CLI.
	EntryOriginal string `json:"entry_original"`
	// Chapters is the ordered list of normalized page URLs visited.
	Chapters []string `json:"chapters"`
	// Pages maps normalized URLs to their page record.
	Pages map[string]*PageRecord `json:"pages"`
	// Failures lists the pages that failed during the last scraping run.
	Failures []FailureRecord `json:"failures"`
}

func newManifest(name, author, entry, entryOriginal string) *Manifest {
	return &Manifest{
		Name:          name,
		Author:        author,
		Entry:         entry,
		EntryOriginal: entryOriginal,
		Chapters:      []string{},
		Pages:         map[string]*PageRecord{},
		Failures:      []FailureRecord{},
	}
}

// manifestPath returns the manifest location inside a book directory.
func manifestPath(bookDir string) string {
	return filepath.Join(bookDir, ManifestName)
}

func loadManifest(bookDir string) (*Manifest, error) {
	data, err := os.ReadFile(manifestPath(bookDir))
	if err != nil {
		return nil, err
	}

	m := &Manifest{}
	if err := json.Unmarshal(data, m); err != nil {
		return nil, err
	}
	if m.Pages == nil {
		m.Pages = map[string]*PageRecord{}
	}
	return m, nil
}

// save writes the manifest atomically: a temporary file is created next to
// the destination and renamed into place, so a crash can never leave a
// half-written manifest behind.
func (m *Manifest) save(bookDir string) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}

	final := manifestPath(bookDir)
	tmp, err := os.CreateTemp(bookDir, ".manifest-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, final); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

// ErrBookNotFound is returned when no stored book matches the entry URL.
var ErrBookNotFound = errors.New("book not found in store")
