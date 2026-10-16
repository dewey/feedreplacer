package main

import (
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/beevik/etree"
)

type fieldValue struct {
	Field string
	Value string
}

type row struct {
	Date    time.Time
	Base    []fieldValue
	Source  []fieldValue
	Matched bool
	// Rule is the 1-based rule that paired the item.
	Rule       int
	RuleFields string
	Changes    []change
}

// change is a replaced field with its old and new value.
type change struct {
	Field string
	// Old is empty if the item did not have the field.
	Old, New string
}

type candidate struct {
	Field   string
	Matched int
}

// suggestion is the best rule order found.
type suggestion struct {
	Rules   [][]string
	Matched int
}

func (s suggestion) Spec() string { return formatRules(s.Rules) }

func (s suggestion) Display() string {
	parts := make([]string, len(s.Rules))
	for i, r := range s.Rules {
		parts[i] = strings.Join(r, " + ")
	}
	return strings.Join(parts, ", then ")
}

// extractStat counts the items an extracted field found a value in.
type extractStat struct {
	Name                   string
	Base, Source           int
	BaseTotal, SourceTotal int
}

// dateField is a field a date can be read from, with the items it has one in.
type dateField struct {
	Field        string
	Base, Source int
}

type analysis struct {
	Total   int
	Matched int
	// SourceTotal is the number of items in the feed to copy from.
	SourceTotal int
	DateFields  []dateField
	// Fields are the fields a rule can use, with the items each pairs alone.
	Fields []candidate
	// ReplaceFields are the item fields of the source feed.
	ReplaceFields []string
	// Rows are the base items, newest first.
	Rows       []row
	Extracts   []extractStat
	Suggestion suggestion
}

func analyze(base, source []byte, o options) (analysis, error) {
	var a analysis
	_, baseChannel, err := parseFeed(base)
	if err != nil {
		return a, err
	}
	_, sourceChannel, err := parseFeed(source)
	if err != nil {
		return a, err
	}
	paired := matchItems(baseChannel, sourceChannel, o.match, o.extract)
	a.Matched = len(paired)

	for _, item := range baseChannel.SelectElements("item") {
		r := row{Date: itemDate(item), Base: displayValues(item, o.extract)}
		if p, ok := paired[item]; ok {
			r.Matched, r.Rule = true, p.rule+1
			r.RuleFields = strings.Join(o.match[p.rule], " + ")
			r.Source = displayValues(p.src, o.extract)
			r.Changes = changes(item, p.src, o.replace)
		}
		a.Rows = append(a.Rows, r)
	}
	slices.SortStableFunc(a.Rows, func(x, y row) int { return y.Date.Compare(x.Date) })

	for _, name := range slices.Sorted(maps.Keys(o.extract)) {
		s := extractStat{Name: name}
		for _, item := range baseChannel.SelectElements("item") {
			s.BaseTotal++
			if o.extract[name].value(item) != "" {
				s.Base++
			}
		}
		for _, item := range sourceChannel.SelectElements("item") {
			s.SourceTotal++
			if o.extract[name].value(item) != "" {
				s.Source++
			}
		}
		a.Extracts = append(a.Extracts, s)
	}

	sourceFields := itemFields(sourceChannel)
	a.Total = len(baseChannel.SelectElements("item"))
	a.SourceTotal = len(sourceChannel.SelectElements("item"))
	a.DateFields, err = dateFields(baseChannel, sourceChannel, sourceFields, o.extract)
	if err != nil {
		return a, err
	}
	a.ReplaceFields = sourceFields
	shared := slices.DeleteFunc(itemFields(baseChannel), func(f string) bool {
		return !slices.Contains(sourceFields, f)
	})
	for name := range o.extract {
		shared = append(shared, name)
	}
	slices.Sort(shared)
	// Title first, so it wins ties.
	slices.SortStableFunc(shared, func(x, y string) int {
		return boolRank(y == defaultMatch) - boolRank(x == defaultMatch)
	})

	baseItems := baseChannel.SelectElements("item")
	sourceItems := sourceChannel.SelectElements("item")
	for _, f := range shared {
		n := len(pairs(baseItems, sourceItems, []string{f}, o.extract))
		a.Fields = append(a.Fields, candidate{Field: f, Matched: n})
	}
	slices.SortStableFunc(a.Fields, func(x, y candidate) int { return strings.Compare(x.Field, y.Field) })
	a.Suggestion = bestRules(baseChannel, sourceChannel, shared, o.extract)
	return a, nil
}

func boolRank(b bool) int {
	if b {
		return 1
	}
	return 0
}

// bestRules adds rules one by one, each time the one that pairs the most
// of the items left, up to three rules.
func bestRules(base, source *etree.Element, shared []string, ex extractors) suggestion {
	var cands [][]string
	for i, f := range shared {
		cands = append(cands, []string{f})
		for _, g := range shared[i+1:] {
			cands = append(cands, []string{f, g})
		}
	}
	var s suggestion
	for len(s.Rules) < 3 {
		var best []string
		for _, c := range cands {
			n := len(matchItems(base, source, append(slices.Clone(s.Rules), c), ex))
			if n > s.Matched {
				best, s.Matched = c, n
			}
		}
		if best == nil {
			break
		}
		s.Rules = append(s.Rules, best)
	}
	return s
}

func itemValues(item *etree.Element, fields []string, ex extractors) []fieldValue {
	values := make([]fieldValue, len(fields))
	for i, f := range fields {
		r := []rune(fieldText(item, f, ex))
		if len(r) > 100 {
			r = append(r[:100], '…')
		}
		values[i] = fieldValue{Field: f, Value: string(r)}
	}
	return values
}

// itemDate is the zero time if the date is missing or not valid.
func itemDate(item *etree.Element) time.Time {
	if e := item.FindElement("./pubDate"); e != nil {
		return parseDate(e.Text())
	}
	return time.Time{}
}

// dateFields lists the fields that hold a date in either feed, pubDate first.
func dateFields(base, source *etree.Element, sourceFields []string, ex extractors) ([]dateField, error) {
	pattern := defaultPattern
	if x, ok := ex[dateName]; ok {
		pattern = x.pattern
	}
	fields := slices.Compact(slices.Sorted(slices.Values(append(itemFields(base), sourceFields...))))
	slices.SortStableFunc(fields, func(x, y string) int {
		return boolRank(y == "pubDate") - boolRank(x == "pubDate")
	})

	var out []dateField
	for _, f := range fields {
		x, err := newExtractor([]string{f}, pattern)
		if err != nil {
			return nil, err
		}
		d := dateField{Field: f}
		for _, item := range base.SelectElements("item") {
			if x.value(item) != "" {
				d.Base++
			}
		}
		for _, item := range source.SelectElements("item") {
			if x.value(item) != "" {
				d.Source++
			}
		}
		if d.Base+d.Source > 0 {
			out = append(out, d)
		}
	}
	return out, nil
}

// displayValues are the fields shown for every item, whatever the rules use.
func displayValues(item *etree.Element, ex extractors) []fieldValue {
	values := itemValues(item, []string{"title", "description"}, ex)
	var day string
	if x, ok := ex[dateName]; ok {
		day = x.value(item)
	} else if t := itemDate(item); !t.IsZero() {
		day = t.Format("2006-01-02")
	}
	return append(values, fieldValue{Field: dateName, Value: day})
}

func changes(item, src *etree.Element, fields []string) []change {
	var out []change
	for _, f := range fields {
		s := src.FindElement("./" + f)
		if s == nil {
			continue
		}
		c := change{Field: f, New: elementText(s)}
		if old := item.FindElement("./" + f); old != nil {
			c.Old = elementText(old)
		}
		if c.Old != c.New {
			out = append(out, c)
		}
	}
	return out
}

// elementText is the text of an element, or its url or attributes if it has
// no text, like an enclosure.
func elementText(e *etree.Element) string {
	text := strings.TrimSpace(e.Text())
	if text == "" {
		text = e.SelectAttrValue("url", "")
	}
	if text == "" {
		for _, a := range e.Attr {
			text += a.Key + "=" + a.Value + " "
		}
	}
	r := []rune(strings.TrimSpace(text))
	if len(r) > 300 {
		r = append(r[:300], '…')
	}
	return string(r)
}
