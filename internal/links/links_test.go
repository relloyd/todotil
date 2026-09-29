package links

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFind(t *testing.T) {
	tests := []struct {
		text string
		want []Span
	}{
		{"no links", nil},
		{"see https://go.dev/doc.", []Span{{Start: 4, End: 22, URL: "https://go.dev/doc"}}},
		{"(https://en.wikipedia.org/wiki/Go_(game))", []Span{{Start: 1, End: 40, URL: "https://en.wikipedia.org/wiki/Go_(game)"}}},
		{"(see http://a.test/x)", []Span{{Start: 5, End: 20, URL: "http://a.test/x"}}},
		{"[Go](https://go.dev) and https://b.test", []Span{
			{Start: 0, End: 20, URL: "https://go.dev", Label: "Go"},
			{Start: 25, End: 39, URL: "https://b.test"},
		}},
		{"ftp://nope.test", nil},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, Find(tt.text), tt.text)
	}
}

func TestReplace(t *testing.T) {
	got := Replace("a https://x.test/p b [lbl](https://y.test) c", func(s Span) string {
		if s.Label != "" {
			return "<" + s.Label + ">"
		}
		return "<" + s.URL + ">"
	})
	assert.Equal(t, "a <https://x.test/p> b <lbl> c", got)
}

func TestParseTitle(t *testing.T) {
	tests := []struct {
		html, want string
	}{
		{"<html><head><title> Hello &amp;\n  World </title></head>", "Hello & World"},
		{`<head><meta property="og:title" content="OG title"></head>`, "OG title"},
		{`<head><title></title><meta property="og:title" content="OG"></head>`, "OG"},
		{"<body><title>too late</title></body>", ""},
		{"<title>" + strings.Repeat("a", 100) + "</title>", strings.Repeat("a", MaxTitleLen-1) + "…"},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, ParseTitle(strings.NewReader(tt.html)), tt.html)
	}
}

func TestFetcherTitle(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte("<title>Fetched</title>"))
		case "/pdf":
			w.Header().Set("Content-Type", "application/pdf")
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	f := NewFetcher()
	ctx := context.Background()

	got, err := f.Title(ctx, srv.URL+"/ok")
	require.NoError(t, err)
	assert.Equal(t, "Fetched", got)

	_, err = f.Title(ctx, srv.URL+"/pdf")
	assert.ErrorIs(t, err, ErrNoTitle)

	_, err = f.Title(ctx, srv.URL+"/missing")
	assert.Error(t, err)
}
