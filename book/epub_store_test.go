package book

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestOfflineEpubUsesStoredImages renders an epub entirely from the
// library and asserts the image bytes inside the epub come from the
// stored files rather than the network.
func TestOfflineEpubUsesStoredImages(t *testing.T) {
	srv, _ := fakeSite(t, nil)
	libRoot := t.TempDir()

	lib, _ := NewTestLibrary(t, libRoot, srv.URL+"/")
	srv.Close() // force fully offline rendering

	found, err := lib.FindBook(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	offline, err := ChapterFromStore(found, srv.URL+"/", false, false)
	if err != nil {
		t.Fatal(err)
	}

	epubPath := filepath.Join(t.TempDir(), "fake.epub")
	ToEpub(offline, epubPath)

	r, err := zip.OpenReader(epubPath)
	if err != nil {
		t.Fatalf("open epub: %v", err)
	}
	defer r.Close()

	imageEntries := 0
	for _, f := range r.File {
		if strings.HasPrefix(f.Name, "EPUB/images/") || strings.Contains(f.Name, "/images/") {
			imageEntries++
			rc, err := f.Open()
			if err != nil {
				t.Fatal(err)
			}
			rc.Close()
		}
	}
	// chapter1 has two images, chapter2 one -> three unique images
	if imageEntries != 3 {
		t.Errorf("expected 3 packaged images, got %d", imageEntries)
	}
}

// TestMarkdownImageReferencePointsToStoredSource checks the markdown keeps
// an http image source whose backing file exists in the library, so the
// same stored image is used by both md and epub without temp files.
func TestMarkdownImageReferencePointsToStoredSource(t *testing.T) {
	srv, _ := fakeSite(t, nil)
	libRoot := t.TempDir()

	lib, _ := NewTestLibrary(t, libRoot, srv.URL+"/")
	srv.Close()

	found, _ := lib.FindBook(srv.URL + "/")
	offline, err := ChapterFromStore(found, srv.URL+"/", false, false)
	if err != nil {
		t.Fatal(err)
	}
	md := ToMarkdownString(offline)
	if !strings.Contains(md, srv.URL+"/img1.png") {
		t.Error("markdown should reference the absolute stored image source")
	}

	// every referenced image must have a library file
	ch1 := found.Manifest.Pages[srv.URL+"/chapter1"]
	for _, img := range ch1.Images {
		if _, ok := found.ImageAbsPath(img.Path); !ok {
			t.Errorf("no library file for referenced image %+v", img)
		}
	}
}

// TestNoTempImageFilesLeftBehind ensures rendering does not create temp
// files in the working directory or OS temp dir.
func TestNoTempImageFilesLeftBehind(t *testing.T) {
	srv, _ := fakeSite(t, nil)
	libRoot := t.TempDir()
	lib, _ := NewTestLibrary(t, libRoot, srv.URL+"/")
	srv.Close()

	tmpBefore, _ := os.ReadDir(os.TempDir())
	found, _ := lib.FindBook(srv.URL + "/")
	offline, _ := ChapterFromStore(found, srv.URL+"/", false, false)
	ToEpub(offline, filepath.Join(t.TempDir(), "x.epub"))
	tmpAfter, _ := os.ReadDir(os.TempDir())

	if len(tmpAfter) > len(tmpBefore) {
		for _, e := range tmpAfter {
			if strings.HasPrefix(e.Name(), "papeer-image") {
				t.Errorf("temp image file leaked: %s", e.Name())
			}
		}
	}
}
