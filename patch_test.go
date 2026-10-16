package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

const baseFeed = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:itunes="http://www.itunes.com/dtds/podcast-1.0.dtd">
  <channel>
    <title>Example Show</title>
    <item>
      <title>Meta’s Plan</title>
      <link>https://base.example/1</link>
      <description>Base description</description>
      <enclosure url="https://base.example/snippet.mp3" length="1" type="audio/mpeg"/>
      <itunes:duration>00:00:17</itunes:duration>
    </item>
    <item>
      <title>Only In Base</title>
      <enclosure url="https://base.example/snippet.mp3" length="1" type="audio/mpeg"/>
    </item>
  </channel>
</rss>`

const sourceFeed = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:itunes="http://www.itunes.com/dtds/podcast-1.0.dtd">
  <channel>
    <title>Other Title</title>
    <item>
      <title><![CDATA[Meta's plan]]></title>
      <description>Source description</description>
      <enclosure url="https://source.example/full.mp3" length="999" type="audio/mpeg"/>
      <itunes:duration>900</itunes:duration>
    </item>
  </channel>
</rss>`

func titleRules() [][]string { return [][]string{{"title"}} }

func TestPatch(t *testing.T) {
	out, err := patch([]byte(baseFeed), []byte(sourceFeed), options{
		match:   titleRules(),
		replace: []string{"enclosure", "itunes:duration"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)

	for _, want := range []string{
		`url="https://source.example/full.mp3"`,
		`<itunes:duration>900</itunes:duration>`,
		`<title>Meta’s Plan</title>`,
		`<description>Base description</description>`,
		`<title>Example Show</title>`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{"snippet.mp3", "Only In Base", "Source description"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("unexpected %q in:\n%s", unwanted, got)
		}
	}
}

func TestPatchNoMatch(t *testing.T) {
	_, err := patch([]byte(baseFeed), []byte(sourceFeed), options{match: [][]string{{"link"}}, replace: []string{"enclosure"}})
	if err == nil {
		t.Fatal("expected error")
	}
}

const sharedAuthorFeed = `<rss version="2.0" xmlns:itunes="http://www.itunes.com/dtds/podcast-1.0.dtd"><channel>
<item><title>One</title><itunes:author>Same</itunes:author></item>
<item><title>Two</title><itunes:author>Same</itunes:author></item>
</channel></rss>`

func TestAnalyzeSkipsAmbiguousFields(t *testing.T) {
	a, err := analyze([]byte(sharedAuthorFeed), []byte(sharedAuthorFeed), options{match: titleRules()})
	if err != nil {
		t.Fatal(err)
	}
	if a.Total != 2 || a.Suggestion.Display() != "title" || a.Suggestion.Matched != 2 {
		t.Fatalf("%+v", a)
	}
}

func TestAnalyzeSortsRowsNewestFirst(t *testing.T) {
	feed := `<rss version="2.0"><channel>
<item><title>Old</title><pubDate>Mon, 01 Jan 2024 09:00:00 +0000</pubDate></item>
<item><title>New</title><pubDate>Tue, 02 Jan 2024 09:00:00 +0000</pubDate></item>
<item><title>Undated</title></item>
</channel></rss>`
	a, err := analyze([]byte(feed), []byte(feed), options{match: titleRules()})
	if err != nil {
		t.Fatal(err)
	}
	if a.Rows[0].Base[0].Value != "New" || a.Rows[1].Base[0].Value != "Old" || a.Rows[2].Base[0].Value != "Undated" {
		t.Fatalf("%+v", a.Rows)
	}
}

const dupTitleFeed = `<rss version="2.0"><channel>
<item><title>Same</title><description>A</description><enclosure url="https://x.example/a.mp3"/></item>
<item><title>Same</title><description>B</description><enclosure url="https://x.example/b.mp3"/></item>
<item><title>Other</title><description>A</description><enclosure url="https://x.example/c.mp3"/></item>
</channel></rss>`

func TestPatchMultipleFields(t *testing.T) {
	a, err := analyze([]byte(dupTitleFeed), []byte(dupTitleFeed), options{match: titleRules()})
	if err != nil {
		t.Fatal(err)
	}
	if a.Matched != 1 || a.Rows[2].Source[0].Value != "Other" {
		t.Fatalf("%+v", a.Rows)
	}
	if a.Total != 3 || a.Suggestion.Display() != "title + description" || a.Suggestion.Matched != 3 {
		t.Fatalf("%+v", a)
	}

	out, err := patch([]byte(dupTitleFeed), []byte(dupTitleFeed), options{match: [][]string{{"title", "description"}}, replace: []string{"enclosure"}})
	if err != nil || !strings.Contains(string(out), "b.mp3") {
		t.Fatalf("err %v, out %s", err, out)
	}
}

const (
	rulesBase = `<rss version="2.0"><channel>
<item><title>One</title><pubDate>Thu, 01 Jan 2026 09:00:00 +0000</pubDate><enclosure url="https://base.example/1.mp3"/></item>
<item><title>Two</title><pubDate>Fri, 02 Jan 2026 09:00:00 +0000</pubDate><enclosure url="https://base.example/2.mp3"/></item>
</channel></rss>`
	rulesSource = `<rss version="2.0"><channel>
<item><title>One</title><enclosure url="https://source.example/1.mp3"/></item>
<item><title>2026-01-02-Example - Two</title><enclosure url="https://source.example/2.mp3"/></item>
</channel></rss>`
)

func TestRulesApplyInOrderWithExtractedDate(t *testing.T) {
	ex, err := parseExtractors([]string{"date=pubDate,title=YYYY-MM-DD"})
	if err != nil {
		t.Fatal(err)
	}
	o := options{match: [][]string{{"title"}, {"date"}}, extract: ex, replace: []string{"enclosure"}}

	a, err := analyze([]byte(rulesBase), []byte(rulesSource), o)
	if err != nil {
		t.Fatal(err)
	}
	if a.Matched != 2 || a.Suggestion.Display() != "title, then date" {
		t.Fatalf("%+v", a)
	}
	if e := a.Extracts[0]; e.Name != "date" || e.Base != 2 || e.Source != 1 || e.SourceTotal != 2 {
		t.Fatalf("%+v", e)
	}
	for _, r := range a.Rows {
		if r.Base[0].Value == "One" && (len(r.Changes) != 1 || r.Changes[0].Old != "https://base.example/1.mp3" || r.Changes[0].New != "https://source.example/1.mp3") {
			t.Fatalf("%+v", r.Changes)
		}
	}
	rules := map[string]int{}
	for _, r := range a.Rows {
		rules[r.Base[0].Value] = r.Rule
	}
	if rules["One"] != 1 || rules["Two"] != 2 {
		t.Fatalf("rules %v", rules)
	}

	out, err := patch([]byte(rulesBase), []byte(rulesSource), o)
	if err != nil || !strings.Contains(string(out), "source.example/1.mp3") || !strings.Contains(string(out), "source.example/2.mp3") {
		t.Fatalf("err %v, out %s", err, out)
	}
}

func TestParseExtractorsRejectsBadSpecs(t *testing.T) {
	for _, spec := range []string{"date", "date=title=YYYY-MM", "=title=YYYY-MM-DD"} {
		if _, err := parseExtractors([]string{spec}); err == nil {
			t.Errorf("expected error for %q", spec)
		}
	}
}

func TestParseRules(t *testing.T) {
	if got := formatRules(parseRules([]string{"title,description|date", "", "guid"})); got != "title,description|date|guid" {
		t.Fatalf("got %q", got)
	}
	if got := formatRules(parseRules(nil)); got != defaultMatch {
		t.Fatalf("got %q", got)
	}
}

func TestFeedHandler(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/base.xml" {
			w.Write([]byte(baseFeed))
			return
		}
		w.Write([]byte(sourceFeed))
	}))
	defer upstream.Close()
	s := &server{client: upstream.Client()}

	q := url.Values{"base": {upstream.URL + "/base.xml"}, "source": {upstream.URL + "/source.xml"}}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/feed?"+q.Encode(), nil)
	rec := httptest.NewRecorder()
	s.feed(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "full.mp3") {
		t.Fatalf("status %d body %s", rec.Code, rec.Body)
	}

	req = httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/feed", nil)
	rec = httptest.NewRecorder()
	s.feed(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d", rec.Code)
	}
}

func TestPreviewHandler(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(sharedAuthorFeed))
	}))
	defer upstream.Close()
	s := &server{client: upstream.Client()}

	q := url.Values{"base": {upstream.URL}, "source": {upstream.URL}, "match": {"itunes:author", ""}}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/preview?"+q.Encode(), nil)
	req.Host = "feeds.example.com"
	rec := httptest.NewRecorder()
	s.preview(rec, req)

	body := rec.Body.String()
	for _, want := range []string{
		"0 of 2 items matched",
		"http://feeds.example.com/feed?",
		"match=itunes%3Aauthor",
		"overcast://x-callback-url/add?url=http%3A%2F%2Ffeeds.example.com%2Ffeed",
		"<legend>Rule 2</legend>",
		`data-size="10"`,
		`data-field="title"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in:\n%s", want, body)
		}
	}
}

func TestIndexFillsFormFromURL(t *testing.T) {
	q := url.Values{
		"base":    {"https://a.example/feed.xml"},
		"source":  {"https://b.example/feed.xml"},
		"extract": {"date=pubDate,title=DD.MM.YYYY"},
		"match":   {"title", "date"},
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/?"+q.Encode(), nil)
	rec := httptest.NewRecorder()
	(&server{}).index(rec, req)

	body := rec.Body.String()
	for _, want := range []string{
		`value="https://a.example/feed.xml"`,
		`value="https://b.example/feed.xml"`,
		`id="pattern" class="text" value="DD.MM.YYYY"`,
		`hx-get="/preview?`,
		"match=title&amp;match=date",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in:\n%s", want, body)
		}
	}
}
