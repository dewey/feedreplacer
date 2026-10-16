package main

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/beevik/etree"
)

// extractor builds a date field from other fields, so items can be matched
// by a date that one feed only has in the title.
type extractor struct {
	from    []string
	pattern string
	re      *regexp.Regexp
	order   []byte
}

const (
	// dateName is the match field the page builds from the date fields.
	dateName       = "date"
	defaultPattern = "YYYY-MM-DD"
)

type extractors map[string]extractor

var dateTokens = regexp.MustCompile(`YYYY|MM|DD`)

// parseExtractors reads specs like "date=pubDate,title=YYYY-MM-DD". Tokens
// outside of the YYYY, MM and DD are matched as they are.
func parseExtractors(specs []string) (extractors, error) {
	out := extractors{}
	for _, spec := range specs {
		name, rest, ok := strings.Cut(spec, "=")
		i := strings.LastIndex(rest, "=")
		if !ok || name == "" || i < 0 {
			return nil, fmt.Errorf("extract %q: want name=from,from=pattern", spec)
		}
		x, err := newExtractor(splitFields(rest[:i]), rest[i+1:])
		if err != nil {
			return nil, fmt.Errorf("extract %q: %w", spec, err)
		}
		out[name] = x
	}
	return out, nil
}

func newExtractor(from []string, pattern string) (extractor, error) {
	x := extractor{from: from, pattern: pattern}
	expr := dateTokens.ReplaceAllStringFunc(regexp.QuoteMeta(pattern), func(tok string) string {
		x.order = append(x.order, tok[0])
		if tok == "YYYY" {
			return `(\d{4})`
		}
		return `(\d{2})`
	})
	if len(x.order) != 3 || strings.Count(string(x.order), "Y") != 1 || strings.Count(string(x.order), "M") != 1 {
		return x, errors.New("pattern needs YYYY, MM and DD once each")
	}
	x.re = regexp.MustCompile(expr)
	return x, nil
}

// value is the day as YYYY-MM-DD. The first source field with a date wins.
func (x extractor) value(item *etree.Element) string {
	for _, f := range x.from {
		e := item.FindElement("./" + f)
		if e == nil {
			continue
		}
		if f == "pubDate" {
			if t := parseDate(e.Text()); !t.IsZero() {
				return t.Format("2006-01-02")
			}
		}
		if m := x.re.FindStringSubmatch(e.Text()); m != nil {
			parts := map[byte]string{}
			for i, c := range x.order {
				parts[c] = m[i+1]
			}
			return parts['Y'] + "-" + parts['M'] + "-" + parts['D']
		}
	}
	return ""
}

func parseDate(s string) time.Time {
	for _, layout := range []string{time.RFC1123Z, time.RFC1123} {
		if t, err := time.Parse(layout, strings.TrimSpace(s)); err == nil {
			return t
		}
	}
	return time.Time{}
}

func fieldText(item *etree.Element, field string, ex extractors) string {
	if x, ok := ex[field]; ok {
		return x.value(item)
	}
	if e := item.FindElement("./" + field); e != nil {
		return strings.TrimSpace(e.Text())
	}
	return ""
}
