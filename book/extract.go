package book

import (
	"fmt"
	urllib "net/url"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/mmcdole/gofeed"
)

// extractHTMLLinks parses an HTML document and returns the links selected
// by selector. It performs no network access: the document is read from
// htmlReader, which makes it usable on freshly fetched pages as well as on
// pages restored from the local library.
func extractHTMLLinks(baseURL *urllib.URL, selector string, limit, offset int, reverse bool, htmlReader strings.Reader) ([]link, string, error) {
	selectorSet := true
	if len(selector) == 0 {
		selector = "a"
		selectorSet = false
	}

	pathLinks := map[string][]link{}
	pathCount := map[string]int{}
	pathMax := ""

	linkHandler := func(s *goquery.Selection) {
		text := strings.TrimSpace(s.Text())
		path := GetPath(s)
		key := path

		hrefStr := s.AttrOr("href", "")
		u, err := baseURL.Parse(hrefStr)
		if err != nil {
			return
		}
		href := u.String()

		if selectorSet {
			// if selector is set, we use the selector specified by the user
			key = selector
			pathLinks[key] = append(pathLinks[key], NewLink(href, text, &time.Time{}))
			pathCount[key] += 1
			pathMax = key
		} else {
			// if selector is not set, we compute the selector ourselves
			class := s.AttrOr("class", "")

			// include the element class to make sure we have the same exact path for every link in the table of content
			key = fmt.Sprintf("%s.%s", path, class)

			// we count this key if the link text is not empty
			if text != "" {
				pathLinks[key] = append(pathLinks[key], NewLink(href, text, &time.Time{}))
				pathCount[key] += len(text)

				if pathCount[key] > pathCount[pathMax] {
					pathMax = key
				}
			}
		}
	}

	doc, err := goquery.NewDocumentFromReader(&htmlReader)
	if err != nil {
		return nil, "", err
	}

	doc.Find(selector).Each(func(i int, s *goquery.Selection) {
		linkHandler(s)
	})

	links := pathLinks[pathMax]
	if len(links) == 0 {
		return nil, pathMax, fmt.Errorf("no link found for selector: %s", selector)
	}

	end := len(links)
	if limit != -1 && limit+offset < end {
		end = limit + offset
	}
	if offset > end {
		offset = end
	}
	links = links[offset:end]

	if reverse {
		for i, j := 0, len(links)-1; i < j; i, j = i+1, j-1 {
			links[i], links[j] = links[j], links[i]
		}
	}

	return links, pathMax, nil
}

// extractFeedLinks parses an RSS/Atom/JSON feed and returns its item links.
// It performs no network access.
func extractFeedLinks(baseURL *urllib.URL, body string) ([]link, error) {
	parser := gofeed.NewParser()
	feed, err := parser.ParseString(body)
	if err != nil {
		return nil, err
	}

	links := make([]link, 0, len(feed.Items))
	for _, item := range feed.Items {
		u, err := baseURL.Parse(item.Link)
		if err != nil {
			return nil, err
		}
		links = append(links, NewLink(u.String(), item.Title, item.PublishedParsed))
	}
	return links, nil
}
