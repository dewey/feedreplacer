package main

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"unicode"

	"github.com/beevik/etree"
)

type options struct {
	// match lists rules in order. A rule pairs items by all of its fields,
	// items left over go to the next rule.
	match   [][]string
	extract extractors
	// replace lists item elements copied from the source feed.
	replace []string
}

const (
	defaultMatch   = "title"
	defaultReplace = "enclosure,itunes:duration"
)

// patch keeps the base feed as is, but copies the replace elements from the
// matching source items. Base items without a match are dropped, because
// they would still point at the old media.
func patch(base, source []byte, o options) ([]byte, error) {
	baseDoc, baseChannel, err := parseFeed(base)
	if err != nil {
		return nil, fmt.Errorf("base feed: %w", err)
	}
	_, sourceChannel, err := parseFeed(source)
	if err != nil {
		return nil, fmt.Errorf("source feed: %w", err)
	}

	paired := matchItems(baseChannel, sourceChannel, o.match, o.extract)
	if len(paired) == 0 {
		return nil, fmt.Errorf("no items matched by %q", formatRules(o.match))
	}
	for _, item := range baseChannel.SelectElements("item") {
		p, ok := paired[item]
		if !ok {
			baseChannel.RemoveChild(item)
			continue
		}
		for _, tag := range o.replace {
			copyElement(item, p.src, tag)
		}
	}

	baseDoc.Indent(2)
	return baseDoc.WriteToBytes()
}

type pair struct {
	src  *etree.Element
	rule int
}

// matchItems applies the rules in order. Items paired by a rule are not
// offered to the later ones.
func matchItems(base, source *etree.Element, rules [][]string, ex extractors) map[*etree.Element]pair {
	out := map[*etree.Element]pair{}
	used := map[*etree.Element]bool{}
	for i, fields := range rules {
		baseItems := slices.DeleteFunc(base.SelectElements("item"), func(e *etree.Element) bool {
			_, ok := out[e]
			return ok
		})
		sourceItems := slices.DeleteFunc(source.SelectElements("item"), func(e *etree.Element) bool {
			return used[e]
		})
		for b, s := range pairs(baseItems, sourceItems, fields, ex) {
			out[b] = pair{src: s, rule: i}
			used[s] = true
		}
	}
	return out
}

// pairs matches base items to source items. Ambiguous keys are skipped, so a
// shared value like the author never pairs everything with one item.
func pairs(base, source []*etree.Element, fields []string, ex extractors) map[*etree.Element]*etree.Element {
	sources := uniqueItems(source, fields, ex)
	out := map[*etree.Element]*etree.Element{}
	for k, b := range uniqueItems(base, fields, ex) {
		if s, ok := sources[k]; ok {
			out[b] = s
		}
	}
	return out
}

func uniqueItems(items []*etree.Element, fields []string, ex extractors) map[string]*etree.Element {
	byKey := map[string]*etree.Element{}
	dups := map[string]bool{}
	for _, item := range items {
		k := key(item, fields, ex)
		if _, ok := byKey[k]; ok {
			dups[k] = true
		}
		if k != "" {
			byKey[k] = item
		}
	}
	for k := range dups {
		delete(byKey, k)
	}
	return byKey
}

// key joins the normalized field values. It is empty if all of them are.
func key(item *etree.Element, fields []string, ex extractors) string {
	parts := make([]string, len(fields))
	for i, f := range fields {
		parts[i] = normalize(fieldText(item, f, ex))
	}
	k := strings.Join(parts, "|")
	if strings.Trim(k, "|") == "" {
		return ""
	}
	return k
}

func normalize(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func splitFields(s string) []string {
	fields := strings.Split(s, ",")
	for i, f := range fields {
		fields[i] = strings.TrimSpace(f)
	}
	return fields
}

// parseRules reads one rule per value, "|" separates rules as well. Empty
// rules are kept, so a new rule in the page stays until it has fields.
func parseRules(values []string) [][]string {
	var rules [][]string
	for _, v := range values {
		for _, r := range strings.Split(v, "|") {
			rule := []string{}
			if strings.TrimSpace(r) != "" {
				rule = splitFields(r)
			}
			rules = append(rules, rule)
		}
	}
	if len(formatRules(rules)) == 0 {
		return [][]string{{defaultMatch}}
	}
	return rules
}

func formatRules(rules [][]string) string {
	var parts []string
	for _, r := range rules {
		if len(r) > 0 {
			parts = append(parts, strings.Join(r, ","))
		}
	}
	return strings.Join(parts, "|")
}

func itemFields(channel *etree.Element) []string {
	seen := map[string]bool{}
	for _, item := range channel.SelectElements("item") {
		for _, e := range item.ChildElements() {
			seen[e.FullTag()] = true
		}
	}
	return slices.Sorted(maps.Keys(seen))
}

func parseFeed(b []byte) (*etree.Document, *etree.Element, error) {
	doc := etree.NewDocument()
	if err := doc.ReadFromBytes(b); err != nil {
		return nil, nil, err
	}
	channel := doc.FindElement("/rss/channel")
	if channel == nil {
		return nil, nil, errors.New("no rss channel found")
	}
	return doc, channel, nil
}

func copyElement(dst, src *etree.Element, tag string) {
	s := src.FindElement("./" + tag)
	if s == nil {
		return
	}
	if old := dst.FindElement("./" + tag); old != nil {
		dst.InsertChildAt(old.Index(), s.Copy())
		dst.RemoveChild(old)
		return
	}
	dst.AddChild(s.Copy())
}
