package candidate

import (
	"fmt"
	"slices"
	"strings"
)

const (
	Preferred = "preferred"
	Possible  = "possible"
	Unwanted  = "unwanted"
	Never     = "never"
)

var BucketNames = [4]string{Preferred, Possible, Unwanted, Never}

type Buckets struct {
	Preferred []string `json:"preferred,omitempty"`
	Possible  []string `json:"possible,omitempty"`
	Unwanted  []string `json:"unwanted,omitempty"`
	Never     []string `json:"never,omitempty"`
}

type PlaceBuckets struct {
	Preferred []Location `json:"preferred,omitempty"`
	Possible  []Location `json:"possible,omitempty"`
	Unwanted  []Location `json:"unwanted,omitempty"`
	Never     []Location `json:"never,omitempty"`
}

func (b Buckets) Lists() [4][]string {
	return [4][]string{b.Preferred, b.Possible, b.Unwanted, b.Never}
}

func (b PlaceBuckets) Lists() [4][]Location {
	return [4][]Location{b.Preferred, b.Possible, b.Unwanted, b.Never}
}

func (b Buckets) IsZero() bool      { return empty(b.Lists()) }
func (b PlaceBuckets) IsZero() bool { return empty(b.Lists()) }

func empty[T any](lists [4][]T) bool {
	return len(lists[0])+len(lists[1])+len(lists[2])+len(lists[3]) == 0
}

func (b Buckets) Of(value string) string {
	for i, list := range b.Lists() {
		if slices.ContainsFunc(list, func(x string) bool { return strings.EqualFold(strings.TrimSpace(x), strings.TrimSpace(value)) }) {
			return BucketNames[i]
		}
	}
	return ""
}

func validateBuckets[T any](what string, lists [4][]T, key func(T) string, refused func(T) string) []string {
	var errs []string
	seen := map[string]bool{}
	for _, list := range lists {
		for _, x := range list {
			k := strings.ToLower(key(x))
			if msg := refused(x); msg != "" {
				errs = append(errs, what+": "+msg)
			} else if seen[k] {
				errs = append(errs, fmt.Sprintf("%s: %s is in more than one list.", what, key(x)))
			}
			seen[k] = true
		}
	}
	return errs
}

func validateOptions(what, choose string, b Buckets, valid ...string) []string {
	return validateBuckets(what, b.Lists(), func(x string) string { return x }, func(x string) string {
		if slices.Contains(valid, x) {
			return ""
		}
		return fmt.Sprintf("choose %s. (got %q)", choose, x)
	})
}

func validatePlaces(b PlaceBuckets) []string {
	return validateBuckets("Locations", b.Lists(), func(l Location) string { return strings.Trim(l.City+", "+l.Country, ", ") }, func(l Location) string {
		if strings.TrimSpace(l.Country) == "" {
			return "choose a country."
		}
		return ""
	})
}
