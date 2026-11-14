package store

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// DefaultDir is the library root used when --store is not provided.
const DefaultDir = "papeer-store"

// PageDir is the directory, inside a book directory, holding page HTML.
const PageDir = "pages"

// ImageDir is the directory, inside a book directory, holding images.
const ImageDir = "images"

// Store is a local library rooted at Root. It is safe for concurrent use.
type Store struct {
	Root string

	mu sync.Mutex
}

// New opens (and if needed creates) a library rooted at root.
func New(root string) (*Store, error) {
	if root == "" {
		root = DefaultDir
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	return &Store{Root: root}, nil
}

// BookStore gives access to one book inside the library.
type BookStore struct {
	store    *Store
	dir      string
	Manifest *Manifest
}

// Dir returns the absolute book directory.
func (b *BookStore) Dir() string {
	return b.dir
}

// bookDirName sanitizes a book title into a directory name.
func bookDirName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "book"
	}
	replacer := strings.NewReplacer(
		"/", "_",
		"\\", "_",
		":", "_",
		"*", "_",
		"?", "_",
		"\"", "_",
		"<", "_",
		">", "_",
		"|", "_",
	)
	return replacer.Replace(name)
}

// FindBook locates the book whose entry URL normalizes to entryURL.
// It returns ErrBookNotFound when no matching book exists in the library.
func (s *Store) FindBook(entryURL string) (*BookStore, error) {
	normalized, err := NormalizeURL(entryURL)
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(s.Root)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		// staging (".book-*") and backup (".old-book-*") directories are hidden
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		dir := filepath.Join(s.Root, entry.Name())
		m, err := loadManifest(dir)
		if err != nil {
			// a directory without a readable manifest is not a book
			continue
		}
		if m.Entry == normalized {
			return &BookStore{store: s, dir: dir, Manifest: m}, nil
		}
	}
	return nil, fmt.Errorf("%w: %s", ErrBookNotFound, entryURL)
}

// CreateBook creates a staging book directory for entryURL. The directory
// receives a temporary name until Commit is called, so an interrupted run
// can never overwrite or half-populate a previously stored book.
func (s *Store) CreateBook(name, author, entryURL string) (*BookStore, error) {
	normalized, err := NormalizeURL(entryURL)
	if err != nil {
		return nil, err
	}

	staging, err := os.MkdirTemp(s.Root, ".book-*")
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(staging, PageDir), 0o755); err != nil {
		os.RemoveAll(staging)
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(staging, ImageDir), 0o755); err != nil {
		os.RemoveAll(staging)
		return nil, err
	}

	m := newManifest(name, author, normalized, entryURL)
	if err := m.save(staging); err != nil {
		os.RemoveAll(staging)
		return nil, err
	}
	return &BookStore{store: s, dir: staging, Manifest: m}, nil
}

// Commit renames the staging directory to its final, book-name based
// location and persists the manifest one last time. An existing book with
// the same directory name is replaced atomically.
func (b *BookStore) Commit() error {
	b.store.mu.Lock()
	defer b.store.mu.Unlock()

	final := filepath.Join(b.store.Root, bookDirName(b.Manifest.Name))
	if err := b.Manifest.save(b.dir); err != nil {
		return err
	}
	if final == b.dir {
		return nil
	}
	if _, err := os.Stat(final); err == nil {
		// swap atomically: move the old book out of the way first
		old, err := os.MkdirTemp(b.store.Root, ".old-book-*")
		if err != nil {
			return err
		}
		// remove the empty staging placeholder so Rename can take its path
		if err := os.Remove(old); err != nil {
			return err
		}
		if err := os.Rename(final, old); err != nil {
			return err
		}
		if err := os.Rename(b.dir, final); err != nil {
			// try to roll back before reporting the failure
			os.Rename(old, final)
			return err
		}
		if err := os.RemoveAll(old); err != nil {
			return err
		}
		b.dir = final
		return nil
	}
	if err := os.Rename(b.dir, final); err != nil {
		return err
	}
	b.dir = final
	return nil
}

// Abort removes a staging book directory. It is safe to call on a
// committed book only when its contents should really be discarded.
func (b *BookStore) Abort() error {
	return os.RemoveAll(b.dir)
}

// pageFileName returns the on-disk file name (under PageDir) for a
// normalized page URL. Same normalized URL always maps to the same file.
func pageFileName(normalizedURL string) string {
	sum := sha256.Sum256([]byte(normalizedURL))
	return hex.EncodeToString(sum[:]) + ".html"
}

// imageFileName returns the on-disk file name (under ImageDir) used for an
// image. The extension is derived from the source URL and restricted to a
// safe whitelist; jpg is used as the default.
func imageFileName(normalizedURL string) string {
	sum := sha256.Sum256([]byte(normalizedURL))
	base := hex.EncodeToString(sum[:])

	ext := strings.ToLower(filepath.Ext(strings.Split(normalizedURL, "?")[0]))
	switch ext {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".svg", ".bmp", ".avif":
	default:
		ext = ".jpg"
	}
	return base + ext
}

// atomicWriteFile writes data to dir/name via a temporary file and a
// rename, guaranteeing readers never observe a half-written file.
func atomicWriteFile(dir, name string, data []byte) (string, int64, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", 0, err
	}
	final := filepath.Join(dir, name)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return "", 0, err
	}
	tmpName := tmp.Name()
	n, err := tmp.Write(data)
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(tmpName)
		return "", 0, err
	}
	if err := os.Rename(tmpName, final); err != nil {
		os.Remove(tmpName)
		return "", 0, err
	}
	return final, int64(n), nil
}

func digestBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// ErrCorruptPage is returned when a stored page is missing, truncated or
// its digest does not match the manifest record.
var ErrCorruptPage = errors.New("stored page is corrupt")

// VerifyPage checks that the HTML file backing record exists, has the
// expected size and matches its recorded content digest.
func (b *BookStore) VerifyPage(record *PageRecord) error {
	if record.Status != StatusSuccess || record.Path == "" {
		return fmt.Errorf("%w: %s has no stored content (status %s)", ErrCorruptPage, record.URL, record.Status)
	}
	data, err := os.ReadFile(filepath.Join(b.dir, record.Path))
	if err != nil {
		return fmt.Errorf("%w: %s: %v", ErrCorruptPage, record.URL, err)
	}
	if int64(len(data)) != record.Bytes {
		return fmt.Errorf("%w: %s: expected %d bytes, got %d", ErrCorruptPage, record.URL, record.Bytes, len(data))
	}
	if digestBytes(data) != record.Digest {
		return fmt.Errorf("%w: %s: content digest mismatch", ErrCorruptPage, record.URL)
	}
	return nil
}

// ReadPage returns the raw HTML stored for normalizedURL after verifying
// its integrity.
func (b *BookStore) ReadPage(normalizedURL string) ([]byte, *PageRecord, error) {
	record, ok := b.Manifest.Pages[normalizedURL]
	if !ok {
		return nil, nil, fmt.Errorf("%w: %s: no manifest record", ErrCorruptPage, normalizedURL)
	}
	if err := b.VerifyPage(record); err != nil {
		return nil, record, err
	}
	data, err := os.ReadFile(filepath.Join(b.dir, record.Path))
	if err != nil {
		return nil, record, fmt.Errorf("%w: %s: %v", ErrCorruptPage, normalizedURL, err)
	}
	return data, record, nil
}

// HasPage reports whether a successful copy of normalizedURL is stored.
func (b *BookStore) HasPage(normalizedURL string) bool {
	record, ok := b.Manifest.Pages[normalizedURL]
	return ok && record.Status == StatusSuccess
}

// Page returns the manifest record for normalizedURL or nil.
func (b *BookStore) Page(normalizedURL string) *PageRecord {
	return b.Manifest.Pages[normalizedURL]
}

// now is a package variable so tests can reason about time deterministically.
var now = func() string { return time.Now().UTC().Format(time.RFC3339) }

// SavePage atomically stores a fetched page and its images and updates the
// manifest in one locked step. Either the whole page is present
// afterwards or nothing at all changed.
func (b *BookStore) SavePage(rec PageRecord, html []byte, images map[string][]byte) error {
	b.store.mu.Lock()
	defer b.store.mu.Unlock()

	pagePath := filepath.Join(PageDir, pageFileName(rec.URL))
	absPath, n, err := atomicWriteFile(filepath.Join(b.dir, PageDir), filepath.Base(pagePath), html)
	if err != nil {
		return err
	}
	_ = absPath

	// store images, recording any that are missing
	imageRecords := make([]ImageRecord, 0, len(rec.Images))
	missing := 0
	for _, img := range rec.Images {
		normalized, err := NormalizeImageURL(img.Source)
		if err != nil {
			normalized = img.Source
		}
		data, ok := images[normalized]
		rel := filepath.ToSlash(filepath.Join(ImageDir, imageFileName(normalized)))
		if !ok {
			missing++
			imageRecords = append(imageRecords, ImageRecord{Source: img.Source, Path: rel, Missing: true})
			continue
		}
		if _, _, err := atomicWriteFile(filepath.Join(b.dir, ImageDir), filepath.Base(rel), data); err != nil {
			return err
		}
		imageRecords = append(imageRecords, ImageRecord{Source: img.Source, Path: rel, Missing: false})
	}

	rec.Path = filepath.ToSlash(pagePath)
	rec.Bytes = n
	rec.Digest = digestBytes(html)
	rec.Images = imageRecords
	rec.MissingImages = missing
	rec.ImageCount = len(imageRecords)
	rec.Status = StatusSuccess
	rec.FetchedAt = now()
	rec.Error = ""

	if _, exists := b.Manifest.Pages[rec.URL]; !exists {
		b.Manifest.Chapters = append(b.Manifest.Chapters, rec.URL)
	}
	b.Manifest.Pages[rec.URL] = &rec

	return b.Manifest.save(b.dir)
}

// RecordFailure records a failed page in the manifest without storing any
// file for it. A previously successful record is left untouched.
func (b *BookStore) RecordFailure(normalizedURL, originalURL, reason string, depth, order int) error {
	b.store.mu.Lock()
	defer b.store.mu.Unlock()

	b.Manifest.Failures = append(b.Manifest.Failures, FailureRecord{
		URL:         normalizedURL,
		OriginalURL: originalURL,
		Error:       reason,
		FailedAt:    now(),
	})

	if existing, ok := b.Manifest.Pages[normalizedURL]; ok && existing.Status == StatusSuccess {
		// keep the last known good page, but remember it failed this run
		return b.Manifest.save(b.dir)
	}

	b.Manifest.Pages[normalizedURL] = &PageRecord{
		URL:         normalizedURL,
		OriginalURL: originalURL,
		Status:      StatusFailed,
		Error:       reason,
		Depth:       depth,
		Order:       order,
		FetchedAt:   now(),
	}
	return b.Manifest.save(b.dir)
}

// ClearFailures resets the failure list at the start of a new run.
func (b *BookStore) ClearFailures() error {
	b.store.mu.Lock()
	defer b.store.mu.Unlock()
	b.Manifest.Failures = []FailureRecord{}
	return b.Manifest.save(b.dir)
}

// FailureURLs returns the normalized URLs recorded as failures.
func (b *BookStore) FailureURLs() []string {
	urls := make([]string, 0, len(b.Manifest.Failures))
	for _, f := range b.Manifest.Failures {
		urls = append(urls, f.URL)
	}
	return urls
}

// ImageAbsPath returns the absolute on-disk path of a stored image and
// whether the file actually exists.
func (b *BookStore) ImageAbsPath(rel string) (string, bool) {
	abs := filepath.Join(b.dir, filepath.FromSlash(rel))
	if _, err := os.Stat(abs); err != nil {
		return abs, false
	}
	return abs, true
}
