// Package links finds URLs in text and fetches page titles so they can be
// shown as short labels.
package links

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/net/html"
)

// Span is a link found in text. Label is set for Markdown links.
type Span struct {
	Start, End int
	URL        string
	Label      string
}

var (
	mdLinkRe = regexp.MustCompile(`\[([^\]\n]+)\]\((https?://[^\s)]+)\)`)
	urlRe    = regexp.MustCompile(`https?://[^\s<>"'\x60\[\]]+`)
)

// Find returns the Markdown links and bare URLs in text, in order.
func Find(text string) []Span {
	var spans []Span
	covered := func(i int) bool {
		for _, s := range spans {
			if i >= s.Start && i < s.End {
				return true
			}
		}
		return false
	}
	for _, m := range mdLinkRe.FindAllStringSubmatchIndex(text, -1) {
		spans = append(spans, Span{Start: m[0], End: m[1], Label: text[m[2]:m[3]], URL: text[m[4]:m[5]]})
	}
	for _, m := range urlRe.FindAllStringIndex(text, -1) {
		if covered(m[0]) {
			continue
		}
		u := trimURL(text[m[0]:m[1]])
		spans = append(spans, Span{Start: m[0], End: m[0] + len(u), URL: u})
	}
	// Keep text order.
	for i := 1; i < len(spans); i++ {
		for j := i; j > 0 && spans[j].Start < spans[j-1].Start; j-- {
			spans[j], spans[j-1] = spans[j-1], spans[j]
		}
	}
	return spans
}

// trimURL drops trailing punctuation that is usually sentence text, keeping
// a closing bracket when the URL contains its opener.
func trimURL(u string) string {
	for len(u) > 0 {
		last := u[len(u)-1]
		switch last {
		case '.', ',', ';', ':', '!', '?', '\'', '"':
			u = u[:len(u)-1]
			continue
		case ')':
			if strings.Count(u, "(") < strings.Count(u, ")") {
				u = u[:len(u)-1]
				continue
			}
		}
		break
	}
	return u
}

// Replace rewrites every link in text with render(span).
func Replace(text string, render func(Span) string) string {
	spans := Find(text)
	if len(spans) == 0 {
		return text
	}
	var b strings.Builder
	prev := 0
	for _, s := range spans {
		b.WriteString(text[prev:s.Start])
		b.WriteString(render(s))
		prev = s.End
	}
	b.WriteString(text[prev:])
	return b.String()
}

// MaxTitleLen caps fetched titles, in runes.
const MaxTitleLen = 60

// CleanTitle collapses whitespace and truncates a title.
func CleanTitle(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) > MaxTitleLen {
		r := []rune(s)
		s = strings.TrimSpace(string(r[:MaxTitleLen-1])) + "…"
	}
	return s
}

// ErrNoTitle is returned when a page has no usable title.
var ErrNoTitle = errors.New("no title")

// Fetcher fetches page titles.
type Fetcher struct {
	Client  *http.Client
	MaxBody int64
}

// NewFetcher returns a Fetcher with a short timeout.
func NewFetcher() *Fetcher {
	return &Fetcher{Client: &http.Client{Timeout: 5 * time.Second}, MaxBody: 1 << 20}
}

// Title fetches url and returns its cleaned page title.
func (f *Fetcher) Title(ctx context.Context, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "todotil (link preview)")
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	resp, err := f.Client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("fetching %s: %s", url, resp.Status)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		mt, _, _ := mime.ParseMediaType(ct)
		if mt != "text/html" && mt != "application/xhtml+xml" {
			return "", ErrNoTitle
		}
	}
	t := ParseTitle(io.LimitReader(resp.Body, f.MaxBody))
	if t == "" {
		return "", ErrNoTitle
	}
	return t, nil
}

// ParseTitle extracts a page title from HTML, preferring <title> and falling
// back to og:title.
func ParseTitle(r io.Reader) string {
	z := html.NewTokenizer(r)
	var title, og string
	inTitle := false
	for {
		switch z.Next() {
		case html.ErrorToken:
			return CleanTitle(cmpOr(title, og))
		case html.StartTagToken, html.SelfClosingTagToken:
			name, hasAttr := z.TagName()
			switch string(name) {
			case "title":
				inTitle = title == ""
			case "meta":
				var prop, content string
				for hasAttr {
					var k, v []byte
					k, v, hasAttr = z.TagAttr()
					switch string(k) {
					case "property", "name":
						prop = string(v)
					case "content":
						content = string(v)
					}
				}
				if prop == "og:title" && og == "" {
					og = content
				}
			case "body":
				return CleanTitle(cmpOr(title, og))
			}
		case html.EndTagToken:
			name, _ := z.TagName()
			switch string(name) {
			case "title":
				inTitle = false
			case "head":
				return CleanTitle(cmpOr(title, og))
			}
		case html.TextToken:
			if inTitle {
				title += string(z.Text())
			}
		}
	}
}

func cmpOr(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}
