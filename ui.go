package main

import (
	"cmp"
	"embed"
	"html/template"
	"net/http"
	"net/url"
	"slices"
	"strings"
)

//go:embed web
var webFS embed.FS

var pages = template.Must(template.New("").
	Funcs(template.FuncMap{
		"has":  slices.Contains[[]string],
		"inc":  func(i int) int { return i + 1 },
		"join": strings.Join,
	}).
	ParseFS(webFS, "web/*.html"))

type appLink struct {
	Name string
	Href template.URL
}

type preview struct {
	analysis
	options
	// Rules and ReplaceSel are the chosen options, for the template.
	Rules      [][]string
	ReplaceSel []string
	// ExtractFrom are the fields the date is read from.
	ExtractFrom []string
	PageSize    int
	Err         string
	URL         string
	Apps        []appLink
}

type indexData struct {
	Base, Source string
	// Query is the page state to load the preview with. It is empty without feeds.
	Query   string
	Pattern string
}

// index fills the form from the url, so a reload keeps everything.
func (s *server) index(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	d := indexData{Base: q.Get("base"), Source: q.Get("source"), Pattern: defaultPattern}
	if d.Base != "" && d.Source != "" {
		d.Query = r.URL.RawQuery
	}
	if ex, err := parseExtractors(q["extract"]); err == nil && ex[dateName].pattern != "" {
		d.Pattern = ex[dateName].pattern
	}
	if err := pages.ExecuteTemplate(w, "index.html", d); err != nil {
		s.fail(w, err)
	}
}

func (s *server) preview(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	baseURL, sourceURL := q.Get("base"), q.Get("source")
	if baseURL == "" || sourceURL == "" {
		return
	}
	var p preview
	var err error
	p.options, err = parseOptions(q)
	p.PageSize = 10
	p.Rules, p.ReplaceSel, p.ExtractFrom = p.match, p.replace, p.extract[dateName].from

	// Errors are shown in the page, so htmx swaps them in.
	if err == nil {
		err = s.fillPreview(r, &p, baseURL, sourceURL)
	}
	if err != nil {
		p.Err = err.Error()
	} else {
		p.URL = feedURL(r, baseURL, sourceURL, p.options, q["extract"])
		p.Apps = appLinks(p.URL)
	}
	if err = pages.ExecuteTemplate(w, "preview.html", p); err != nil {
		s.fail(w, err)
	}
}

func (s *server) fillPreview(r *http.Request, p *preview, baseURL, sourceURL string) error {
	base, err := s.fetch(r, baseURL)
	if err != nil {
		return err
	}
	var source []byte
	source, err = s.fetch(r, sourceURL)
	if err != nil {
		return err
	}
	p.analysis, err = analyze(base, source, p.options)
	return err
}

// appLinks are the subscribe deep links of the podcast apps. The apps fetch
// the feed themselves, so the url must be reachable from the phone.
func appLinks(feed string) []appLink {
	bare := strings.TrimPrefix(strings.TrimPrefix(feed, "https://"), "http://")
	return []appLink{
		{"Overcast", template.URL("overcast://x-callback-url/add?url=" + url.QueryEscape(feed))},
		{"Apple Podcasts", template.URL("podcast://" + bare)},
		{"Pocket Casts", template.URL("pktc://subscribe/" + bare)},
		{"Castro", template.URL("castro://subscribe/" + bare)},
	}
}

func feedURL(r *http.Request, baseURL, sourceURL string, o options, extract []string) string {
	scheme := cmp.Or(r.Header.Get("X-Forwarded-Proto"), "http")
	if r.TLS != nil {
		scheme = "https"
	}
	q := url.Values{"base": {baseURL}, "source": {sourceURL}, "extract": extract}
	if match := formatRules(o.match); match != defaultMatch {
		q.Set("match", match)
	}
	if replace := strings.Join(o.replace, ","); replace != defaultReplace {
		q.Set("replace", replace)
	}
	u := url.URL{Scheme: scheme, Host: r.Host, Path: "/feed", RawQuery: q.Encode()}
	return u.String()
}
