// Package store implements the local on-disk library used to persist
// scraped pages and images so rendering can be performed offline.
package store

import (
	"net"
	urllib "net/url"
	"strings"
)

// NormalizeURL returns the canonical representation of a page URL.
//
// URLs that only differ by fragment, a trailing slash on the path, host
// letter case or a default port resolve to the same normalized URL and
// therefore map to the same stored page. Query strings are preserved.
func NormalizeURL(raw string) (string, error) {
	u, err := urllib.Parse(raw)
	if err != nil {
		return "", err
	}

	// fragments never identify a page
	u.Fragment = ""

	// host names are case insensitive
	u.Host = strings.ToLower(u.Host)

	// drop default ports so that http://host:80/x and http://host/x match
	if host, port, err := net.SplitHostPort(u.Host); err == nil {
		if (u.Scheme == "http" && port == "80") || (u.Scheme == "https" && port == "443") {
			u.Host = host
		}
	}

	// a single trailing slash (or several) does not identify a different page,
	// but the root path "/" is kept as-is
	if u.Path != "/" {
		u.Path = strings.TrimRight(u.Path, "/")
		if u.Path == "" {
			u.Path = "/"
		}
	}

	return u.String(), nil
}

// NormalizeImageURL canonicalizes an image URL so that the same image
// referenced from different pages is stored only once.
func NormalizeImageURL(raw string) (string, error) {
	return NormalizeURL(raw)
}
