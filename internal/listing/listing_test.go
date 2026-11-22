package listing_test

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/sifty-works/sifty-server/internal/listing"
)

func TestCanonical(t *testing.T) {
	for _, tc := range []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{"lowercases scheme and host", "HTTPS://Example.COM/jobs/42", "https://example.com/jobs/42", false},
		{"drops www", "https://www.example.com/jobs", "https://example.com/jobs", false},
		{"drops fragment", "https://example.com/jobs/1#apply", "https://example.com/jobs/1", false},
		{"drops utm params", "https://example.com/jobs?utm_source=linkedin&utm_campaign=foo", "https://example.com/jobs", false},
		{"drops ref source gh_src", "https://example.com/jobs/1?ref=board&source=web&gh_src=abc&keep=1", "https://example.com/jobs/1?keep=1", false},
		{"sorts remaining query", "https://example.com/jobs?z=last&a=first", "https://example.com/jobs?a=first&z=last", false},
		{"drops trailing slash", "https://example.com/jobs/", "https://example.com/jobs", false},
		{"empty path becomes slash", "https://example.com", "https://example.com/", false},
		{"rejects non-HTTP", "ftp://example.com/file", "", true},
		{"case-insensitive param dropping", "https://example.com/jobs?UTM_SOURCE=x&REF=y&id=42", "https://example.com/jobs?id=42", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := listing.Canonical(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Errorf("Canonical(%q): expected error", tc.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("Canonical(%q): %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("Canonical(%q): got %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestIdentity(t *testing.T) {
	for _, tc := range []struct{ company, title, want string }{
		{"Acme Corp", "Software Engineer", "acme corp|software engineer"},
		{"Widgets", "Node.js Developer", "widgets|nodejs developer"},
		{"Widgets", "nodejs Developer", "widgets|nodejs developer"},
		{"Widgets", "Web3.js Lead", "widgets|web3js lead"},
		{"Widgets", "Layer 2 Engineer", "widgets|l2 engineer"},
		{"Widgets", "Chief Technology Officer", "widgets|cto"},
		{"Widgets", "Technical Lead", "widgets|tech lead"},
		{"Widgets", "Artificial Intelligence Researcher", "widgets|ai researcher"},
		{"Acme, Inc.", "Sr. Engineer (Remote)", "acme inc|sr engineer remote"},
		{"  Acme  ", "  Engineer  ", "acme|engineer"},
	} {
		got := listing.Identity(tc.company, tc.title)
		if got != tc.want {
			t.Errorf("Identity(%q, %q): got %q, want %q", tc.company, tc.title, got, tc.want)
		}
	}
}

func hexDigest(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func TestLegacyKey(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{
			"percent-encoded path preserved",
			"https://example.com/jobs/software%20engineer",
			hexDigest("url:https://example.com/jobs/software%20engineer"),
		},
		{
			"empty query value written as k=",
			"https://example.com/jobs?track=&id=42",
			hexDigest("url:https://example.com/jobs?id=42&track="),
		},
		{
			"double trailing slash: only one removed",
			"https://example.com/jobs//",
			hexDigest("url:https://example.com/jobs/"),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := listing.LegacyKey(tc.raw)
			if got != tc.want {
				t.Errorf("LegacyKey(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}
