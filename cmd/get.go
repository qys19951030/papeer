package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lapwat/papeer/book"
	"github.com/lapwat/papeer/book/store"
)

type GetOptions struct {
	// url string

	name   string
	author string
	Format string
	output string
	stdout bool
	images bool
	quiet  bool

	Selector    []string
	depth       int
	limit       int
	offset      int
	reverse     bool
	delay       int
	threads     int
	include     bool
	useLinkName bool
	printURL    bool
	browser     bool

	storeDir  string
	fromStore bool
	refresh   bool
}

var getOpts *GetOptions

func init() {
	getOpts = &GetOptions{}

	getCmd.Flags().StringVarP(&getOpts.name, "name", "n", "", "book name (default: page title)")
	getCmd.Flags().StringVarP(&getOpts.author, "author", "a", "", "book author")
	getCmd.Flags().StringVarP(&getOpts.Format, "format", "f", "md", "file format [md, json, html, epub, mobi]")
	getCmd.Flags().StringVarP(&getOpts.output, "output", "", "", "file name (default: book name)")
	getCmd.Flags().BoolVarP(&getOpts.stdout, "stdout", "", false, "print to standard output")
	getCmd.Flags().BoolVarP(&getOpts.images, "images", "", false, "retrieve images only")
	getCmd.Flags().BoolVarP(&getOpts.printURL, "print-url", "", false, "print url after chapter title")
	getCmd.Flags().BoolVarP(&getOpts.quiet, "quiet", "q", false, "hide progress bar")

	// local library
	getCmd.Flags().StringVarP(&getOpts.storeDir, "store", "", store.DefaultDir, "local library root directory")
	getCmd.Flags().BoolVarP(&getOpts.fromStore, "from-store", "", false, "render offline from the local library, no network access")
	getCmd.Flags().BoolVarP(&getOpts.refresh, "refresh", "", false, "re-fetch pages already stored in the library")

	// common with list command
	getCmd.Flags().StringSliceVarP(&getOpts.Selector, "selector", "s", []string{}, "table of contents CSS selector")
	getCmd.Flags().IntVarP(&getOpts.depth, "depth", "d", 0, "scraping depth")
	getCmd.Flags().IntVarP(&getOpts.limit, "limit", "l", -1, "limit number of chapters, use with depth/selector")
	getCmd.Flags().IntVarP(&getOpts.offset, "offset", "o", 0, "skip first chapters, use with depth/selector")
	getCmd.Flags().BoolVarP(&getOpts.reverse, "reverse", "r", false, "reverse chapter order")
	getCmd.Flags().IntVarP(&getOpts.delay, "delay", "", -1, "time in milliseconds to wait before downloading next chapter, use with depth/selector")
	getCmd.Flags().IntVarP(&getOpts.threads, "threads", "t", -1, "download concurrency, use with depth/selector")
	getCmd.Flags().BoolVarP(&getOpts.include, "include", "i", false, "include URL as first chapter, use with depth/selector")
	getCmd.Flags().BoolVarP(&getOpts.useLinkName, "use-link-name", "", false, "use link name for chapter title")
	getCmd.Flags().BoolVarP(&getOpts.browser, "browser", "b", false, "use headless browser to load pages")

	rootCmd.AddCommand(getCmd)
}

var getCmd = &cobra.Command{
	Use:     "get URL",
	Short:   "Scrape URL content",
	Example: "papeer get https://www.eff.org/cyberspace-independence",
	Args: func(cmd *cobra.Command, args []string) error {
		if len(args) < 1 {
			return errors.New("requires an URL argument")
		}

		// check provided format is in list
		formatEnum := map[string]bool{
			"md":   true,
			"json": true,
			"html": true,
			"epub": true,
			"mobi": true,
		}
		if formatEnum[getOpts.Format] != true {
			return fmt.Errorf("invalid format specified: %s", getOpts.Format)
		}

		if getOpts.fromStore && getOpts.refresh {
			return errors.New("cannot use --refresh together with --from-store")
		}

		// add .mobi to filename if not specified
		if getOpts.Format == "mobi" {
			if len(getOpts.output) > 0 && strings.HasSuffix(getOpts.output, ".mobi") == false {
				getOpts.output = fmt.Sprintf("%s.mobi", getOpts.output)
			}
		}

		// increase depth to match limit
		if cmd.Flags().Changed("limit") && getOpts.depth == 0 {
			getOpts.depth = 1
		}

		// fill selector array with empty selectors to match depth
		getOpts.Selector = append(getOpts.Selector, "")
		for len(getOpts.Selector) < getOpts.depth+1 {
			getOpts.Selector = append(getOpts.Selector, "")
		}

		if cmd.Flags().Changed("include") && getOpts.depth == 0 && len(getOpts.Selector) == 0 {
			return errors.New("cannot use include option if depth/selector is not specified")
		}

		if cmd.Flags().Changed("offset") && getOpts.depth == 0 && len(getOpts.Selector) == 0 {
			return errors.New("cannot use offset option if depth/selector is not specified")
		}

		if cmd.Flags().Changed("reverse") && getOpts.depth == 0 && len(getOpts.Selector) == 0 {
			return errors.New("cannot use reverse option if depth/selector is not specified")
		}

		if cmd.Flags().Changed("delay") && getOpts.depth == 0 && len(getOpts.Selector) == 0 {
			return errors.New("cannot use delay option if depth/selector is not specified")
		}

		if cmd.Flags().Changed("threads") && getOpts.depth == 0 && len(getOpts.Selector) == 0 {
			return errors.New("cannot use threads option if depth/selector is not specified")
		}

		if cmd.Flags().Changed("use-link-name") && getOpts.depth == 0 && len(getOpts.Selector) == 0 {
			return errors.New("cannot use use-link-name option if depth/selector is not specified")
		}

		if cmd.Flags().Changed("delay") && cmd.Flags().Changed("threads") {
			return errors.New("cannot use delay and threads options at the same time")
		}

		return nil
	},
	Run: func(cmd *cobra.Command, args []string) {
		var (
			scraped    book.Chapter
			failedURLs []string
			storeBook  *store.BookStore
		)

		if getOpts.fromStore {
			// offline rendering: locate the book in the library and rebuild
			// the chapter tree without any network access
			lib, err := store.New(getOpts.storeDir)
			if err != nil {
				log.Fatal(err)
			}
			storeBook, err = lib.FindBook(args[0])
			if err != nil {
				log.Fatal(err)
			}
			root, err := book.ChapterFromStore(storeBook, args[0], getOpts.printURL, getOpts.images)
			if err != nil {
				log.Fatal(err)
			}
			scraped = root
			failedURLs = storeBook.FailureURLs()
		} else {
			// generate config for each level
			configs := make([]*book.ScrapeConfig, len(getOpts.Selector))
			for index, s := range getOpts.Selector {
				config := book.NewScrapeConfig()
				config.Selector = s
				config.Quiet = getOpts.quiet
				config.Limit = getOpts.limit
				config.Offset = getOpts.offset
				config.Reverse = getOpts.reverse
				config.Delay = getOpts.delay
				config.Threads = getOpts.threads
				config.ImagesOnly = getOpts.images
				config.Include = getOpts.include
				config.UseLinkName = getOpts.useLinkName
				config.PrintURL = getOpts.printURL
				config.Browser = getOpts.browser

				// do not use link name for root level as there is not parent link
				if index == 0 {
					config.UseLinkName = false
				}

				// always include last level by default
				if index == len(getOpts.Selector)-1 {
					config.Include = true
				}

				configs[index] = config
			}

			lib, err := store.New(getOpts.storeDir)
			if err != nil {
				log.Fatal(err)
			}
			entry := args[0]

			// reuse an existing book directory when present, otherwise stage
			// a new one; the directory only becomes visible on Commit
			existing, findErr := lib.FindBook(entry)
			if findErr == nil {
				storeBook = existing
			} else {
				storeBook, err = lib.CreateBook(getOpts.name, getOpts.author, entry)
				if err != nil {
					log.Fatal(err)
				}
			}

			// scrape all entry URLs into the same library book
			dummy := book.NewEmptyChapter()
			entryOK := false
			for _, u := range args {
				newChapter, failures, scrapeErr := book.ScrapeToStore(storeBook, u, configs, getOpts.refresh)
				failedURLs = append(failedURLs, failures...)
				if scrapeErr != nil {
					// entry page failed: it is recorded in the manifest, do
					// not add an empty chapter to the rendered tree
					continue
				}
				dummy.AddSubChapter(newChapter)
				entryOK = true
			}

			if !entryOK {
				// every entry failed: persist the failure records and bail
				if err := storeBook.Commit(); err != nil {
					log.Fatal(err)
				}
				for _, u := range dedupe(failedURLs) {
					fmt.Fprintf(os.Stderr, "failed to scrape page: %s\n", u)
				}
				os.Exit(1)
			}

			// book metadata: prefer explicit flags, fall back to the first
			// scraped chapter
			if getOpts.name != "" {
				dummy.SetName(getOpts.name)
			} else {
				dummy.SetName(dummy.SubChapters()[0].Name())
			}
			storeBook.Manifest.Name = dummy.Name()

			if getOpts.author != "" {
				dummy.SetAuthor(getOpts.author)
			} else {
				dummy.SetAuthor(dummy.SubChapters()[0].Author())
			}
			storeBook.Manifest.Author = dummy.Author()

			if err := storeBook.Commit(); err != nil {
				log.Fatal(err)
			}
			scraped = dummy
		}

		renderBook(scraped, failedURLs)
	},
}

// renderBook writes the requested output and applies the non-zero exit
// code contract when at least one page failed.
func renderBook(c book.Chapter, failedURLs []string) {
	if getOpts.Format == "md" {
		filename := book.ToMarkdown(c, getOpts.output)

		if getOpts.stdout {
			bytesRead, err := ioutil.ReadFile(filename)
			if err != nil {
				log.Fatal(err)
			}
			os.Remove(filename)

			fmt.Println(string(bytesRead))
		} else {
			fmt.Printf("Markdown saved to \"%s\"\n", filename)
		}
	}

	if getOpts.Format == "json" {
		filename := book.ToMarkdown(c, getOpts.output)

		bytesRead, err := ioutil.ReadFile(filename)
		if err != nil {
			log.Fatal(err)
		}
		os.Remove(filename)

		book := make(map[string]interface{})
		book["name"] = c.Name()
		book["author"] = c.Author()
		book["content"] = string(bytesRead)

		bookJson, err := json.Marshal(book)
		if err != nil {
			log.Fatal(err)
		}

		fmt.Println(string(bookJson))
	}

	if getOpts.Format == "html" {
		filename := book.ToHtml(c, getOpts.output)

		if getOpts.stdout {
			bytesRead, err := ioutil.ReadFile(filename)
			if err != nil {
				log.Fatal(err)
			}
			os.Remove(filename)

			fmt.Println(string(bytesRead))
		} else {
			fmt.Printf("Html saved to \"%s\"\n", filename)
		}
	}

	if getOpts.Format == "epub" {
		filename := book.ToEpub(c, getOpts.output)

		if getOpts.stdout {
			bytesRead, err := ioutil.ReadFile(filename)
			if err != nil {
				log.Fatal(err)
			}
			os.Remove(filename)

			fmt.Println(string(bytesRead))
		} else {
			fmt.Printf("Ebook saved to \"%s\"\n", filename)
		}
	}

	if getOpts.Format == "mobi" {
		filename := book.ToMobi(c, getOpts.output)

		if getOpts.stdout {
			bytesRead, err := ioutil.ReadFile(filename)
			if err != nil {
				log.Fatal(err)
			}
			os.Remove(filename)

			fmt.Println(string(bytesRead))
		} else {
			fmt.Printf("Ebook saved to \"%s\"\n", filename)
		}
	}

	if len(failedURLs) > 0 {
		// failures never abort rendering: the successful part of the book
		// has already been written above. Report and exit non-zero.
		for _, u := range dedupe(failedURLs) {
			fmt.Fprintf(os.Stderr, "failed to scrape page: %s\n", u)
		}
		os.Exit(1)
	}
}

func dedupe(items []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(items))
	for _, it := range items {
		if seen[it] {
			continue
		}
		seen[it] = true
		out = append(out, it)
	}
	return out
}
