package book

// Chapter is the public view of a scraped chapter. It is an alias of the
// package-internal chapter type so external callers (the CLI) can pass
// chapters around without exposing the struct fields.
type Chapter = chapter

// NewEmptyChapter is already exported; NewRootChapter builds the dummy
// container used to aggregate several entry URLs into one book.
func NewRootChapter() Chapter {
	return NewEmptyChapter()
}
