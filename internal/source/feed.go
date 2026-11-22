package source

import (
	"bytes"
	"encoding/xml"
	"strings"

	"github.com/sifty-works/sifty-server/internal/listing"
)

func parseFeedXML(body []byte, base string) ([]listing.Posting, error) {
	dec := xml.NewDecoder(bytes.NewReader(body))
	dec.Strict = false
	for {
		tok, err := dec.Token()
		if err != nil {
			return nil, ErrNothingRecognised
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		switch se.Name.Local {
		case "rss":
			var feed rssFeed
			if err := dec.DecodeElement(&feed, &se); err != nil {
				return nil, ErrNothingRecognised
			}
			items := make([]feedItem, 0, len(feed.Items))
			for _, item := range feed.Items {
				text := item.Encoded
				if text == "" {
					text = item.Desc
				}
				title, company := splitCompanyTitle(trim(item.Title), trim(item.Company))
				items = append(items, feedItem{
					url: item.Link, title: title, company: company,
					tags: item.Categories, text: text,
					published: item.PubDate, pubBasis: "pubDate",
					region:     trim(stripSMPChars(item.Region)),
					country:    trim(stripSMPChars(item.Country)),
					location:   item.Location,
					employType: item.Type,
					expiresAt:  item.ExpiresAt,
				})
			}
			return buildPostings(items, base), nil
		case "feed":
			var feed atomFeed
			if err := dec.DecodeElement(&feed, &se); err != nil {
				return nil, ErrNothingRecognised
			}
			items := make([]feedItem, 0, len(feed.Entries))
			for _, e := range feed.Entries {
				link := ""
				for _, l := range e.Links {
					if link == "" {
						link = l.Href
					}
					if l.Rel == "alternate" {
						link = l.Href
						break
					}
				}
				text := e.Content
				if text == "" {
					text = e.Summary
				}
				var cats []string
				for _, c := range e.Categories {
					if t := c.Term; t != "" {
						cats = append(cats, t)
					} else if c.Label != "" {
						cats = append(cats, c.Label)
					}
				}
				title, company := splitCompanyTitle(trim(e.Title), trim(e.Company))
				items = append(items, feedItem{
					url: link, title: title, company: company,
					tags: cats, text: text,
					published: e.Published, pubBasis: "published",
				})
			}
			return buildPostings(items, base), nil
		default:
			return nil, ErrNothingRecognised
		}
	}
}

type feedItem struct {
	url        string
	title      string
	company    string
	tags       []string
	text       string
	published  string
	pubBasis   string
	location   string
	region     string
	country    string
	employType string
	expiresAt  string
}

func buildPostings(items []feedItem, base string) []listing.Posting {
	var out []listing.Posting
	for _, it := range items {
		p := listing.Posting{
			URL:     it.url,
			Title:   it.title,
			Company: it.company,
			Tags:    it.tags,
			Text:    it.text,
		}
		switch {
		case it.country != "":
			p.Location = it.country
		case it.region != "":
			p.Location = it.region
		case it.location != "":
			p.Location = it.location
		}
		p.Fields = setFields("employment_type", it.employType)
		if pub, ok := parseDate(it.published); ok {
			p.Published = pub
			p.PublishedBasis = it.pubBasis
		}
		if exp, ok := parseDate(it.expiresAt); ok {
			p.Expires = exp
		}
		if p, ok := finalise(p, base); ok {
			out = append(out, p)
		}
	}
	return out
}

type rssFeed struct {
	Items []rssItem `xml:"channel>item"`
}

type rssItem struct {
	Title      string   `xml:"title"`
	Link       string   `xml:"link"`
	PubDate    string   `xml:"pubDate"`
	Desc       string   `xml:"description"`
	Encoded    string   `xml:"http://purl.org/rss/1.0/modules/content/ encoded"`
	Company    string   `xml:"company"`
	Categories []string `xml:"category"`
	Type       string   `xml:"type"`
	Region     string   `xml:"region"`
	Country    string   `xml:"country"`
	Location   string   `xml:"location"`
	ExpiresAt  string   `xml:"expires_at"`
}

type atomFeed struct {
	Entries []atomEntry `xml:"entry"`
}

type atomEntry struct {
	Title      string         `xml:"title"`
	Links      []atomLink     `xml:"link"`
	Published  string         `xml:"published"`
	Content    string         `xml:"content"`
	Summary    string         `xml:"summary"`
	Company    string         `xml:"company"`
	Categories []atomCategory `xml:"category"`
}

type atomLink struct {
	Rel  string `xml:"rel,attr"`
	Href string `xml:"href,attr"`
}

type atomCategory struct {
	Term  string `xml:"term,attr"`
	Label string `xml:"label,attr"`
}

func splitCompanyTitle(title, company string) (string, string) {
	if company != "" {
		return title, company
	}
	if idx := strings.Index(title, ": "); idx >= 0 {
		return title[idx+2:], title[:idx]
	}
	if idx := strings.LastIndex(title, " at "); idx >= 0 {
		return title[:idx], title[idx+4:]
	}
	return title, ""
}

func stripSMPChars(s string) string {
	return strings.Map(func(r rune) rune {
		if r > 0xFFFF {
			return -1
		}
		return r
	}, s)
}
