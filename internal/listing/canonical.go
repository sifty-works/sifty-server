package listing

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

func Canonical(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("listing: canonical: %w", err)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("listing: canonical: unsupported scheme %q", u.Scheme)
	}
	host := strings.ToLower(u.Host)
	host = strings.TrimPrefix(host, "www.")

	q := u.Query()
	for k := range q {
		if dropParam(k) {
			delete(q, k)
		}
	}

	u.Scheme = scheme
	u.Host = host
	u.Fragment = ""
	u.RawQuery = q.Encode()

	ep := u.EscapedPath()
	if ep == "" {
		ep = "/"
	} else if ep != "/" {
		ep = strings.TrimSuffix(ep, "/")
	}
	u.RawPath = ep

	out := scheme + "://" + host + ep
	if u.RawQuery != "" {
		out += "?" + u.RawQuery
	}
	return out, nil
}

var utmRE = regexp.MustCompile(`(?i)^utm_`)

func dropParam(key string) bool {
	lower := strings.ToLower(key)
	return utmRE.MatchString(lower) || lower == "ref" || lower == "source" || lower == "gh_src"
}
