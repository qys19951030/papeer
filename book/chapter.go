package book

type chapter struct {
	url         string
	body        string
	name        string
	author      string
	content     string
	subChapters []chapter
	config      *ScrapeConfig

	// storeRoot is the absolute directory of the backing BookStore when
	// the chapter was built from (or against) a local library. Empty for
	// legacy in-memory scrapes.
	storeRoot string
	// images maps normalized image source URLs to their store-relative
	// paths. Missing images are absent from the map.
	images map[string]string
	// imageData holds freshly downloaded image bytes keyed by normalized
	// source, handed to the store for atomic persistence.
	imageData map[string][]byte
	// missingImages lists normalized image sources that failed to download.
	missingImages []string
	// depth is the chapter tree depth used when persisting the page.
	depth int
	// parent is the normalized URL of the parent page, empty for the entry.
	parent string
}

func NewEmptyChapter() chapter {
	return chapter{"", "", "", "", "", []chapter{}, NewScrapeConfigNoInclude(), "", nil, nil, nil, 0, ""}
}

func (c chapter) URL() string {
	return c.url
}

func (c chapter) Body() string {
	return c.body
}

func (c chapter) Name() string {
	return c.name
}

func (c *chapter) SetName(name string) {
	c.name = name
}

func (c chapter) Author() string {
	return c.author
}

func (c *chapter) SetAuthor(author string) {
	c.author = author
}

func (c chapter) Content() string {
	return c.content
}

func (c chapter) SubChapters() []chapter {
	return c.subChapters
}

func (c *chapter) AddSubChapter(newChapter chapter) {
	c.subChapters = append(c.subChapters, newChapter)
}

// StoreRoot returns the backing library directory, empty for legacy scrapes.
func (c chapter) StoreRoot() string {
	return c.storeRoot
}

// Images returns the normalized-source -> store-relative-path image map.
func (c chapter) Images() map[string]string {
	return c.images
}

// MissingImages returns normalized image sources that could not be fetched.
func (c chapter) MissingImages() []string {
	return c.missingImages
}

// RebindStoreRoot walks the whole chapter tree and points every chapter's
// library directory at dir. It is called after a BookStore.Commit, which
// renames the staging directory into its final location, so rendering never
// resolves images through a stale staging path.
func (c *chapter) RebindStoreRoot(dir string) {
	c.storeRoot = dir
	for i := range c.subChapters {
		c.subChapters[i].RebindStoreRoot(dir)
	}
}
