package fit

import (
	"slices"

	"github.com/sifty-works/sifty-server/internal/candidate"
	"github.com/sifty-works/sifty-server/internal/facts"
	"github.com/sifty-works/sifty-server/internal/places"
)

var (
	bucketUtility = map[string]float64{candidate.Preferred: 1, candidate.Possible: 0.75, candidate.Unwanted: 0.25, candidate.Never: 0}
	bucketWords   = map[string]string{candidate.Preferred: "preferred", candidate.Possible: "possible", candidate.Unwanted: "unwanted", candidate.Never: "in your Never list"}
)

type hit struct{ label, bucket string }

func rank(bucket string) int {
	if i := slices.Index(candidate.BucketNames[:], bucket); i >= 0 {
		return i
	}
	return len(candidate.BucketNames)
}

func valueHits(b candidate.Buckets, values []string) []hit {
	var hits []hit
	for _, v := range values {
		hits = append(hits, hit{sentence(list([]string{v}, "")), b.Of(v)})
	}
	return hits
}

func bestHit(hits []hit) hit {
	best := hits[0]
	for _, h := range hits[1:] {
		if rank(h.bucket) < rank(best.bucket) {
			best = h
		}
	}
	return best
}

func bucketVerdict(hits []hit, src facts.Source, unstated string) Verdict {
	if len(hits) == 0 {
		return Verdict{Outcome: Unknown, Source: src, Reason: unstated}
	}
	best := bestHit(hits)
	if best.bucket == "" {
		var labels []string
		for _, h := range hits {
			labels = append(labels, h.label)
		}
		return Verdict{Outcome: Skipped, Source: src, Reason: sentence(either(labels)) + " — not in your lists"}
	}
	u := bucketUtility[best.bucket]
	outcome := Match
	if u < 0.5 {
		outcome = Mismatch
	}
	return Verdict{Outcome: outcome, Utility: u, Source: src, Reason: sentence(best.label) + " — " + bucketWords[best.bucket], Bucket: best.bucket}
}

func placeBucket(b candidate.PlaceBuckets, pl places.Place) string {
	lists := b.Lists()
	for i := len(lists) - 1; i >= 0; i-- {
		lenient := candidate.BucketNames[i] == candidate.Preferred || candidate.BucketNames[i] == candidate.Possible
		if slices.ContainsFunc(lists[i], func(l candidate.Location) bool { return inPlace(l, pl, lenient) }) {
			return candidate.BucketNames[i]
		}
	}
	return ""
}

func inPlace(entry candidate.Location, pl places.Place, lenient bool) bool {
	codes := places.Codes(entry.Country)
	if codes == nil {
		return entry.City != "" && has(pl.Cities, entry.City)
	}
	sameCity := entry.City == "" || has(pl.Cities, entry.City) || lenient && len(pl.Cities) == 0
	return overlap(codes, pl.Countries) == 1 && sameCity
}

func weekly(h *candidate.Hours, vh *Hours) float64 {
	if h == nil || vh == nil || vh.Mode != "part_time" {
		return na
	}
	lo, hi := h.WeeklyMin, h.WeeklyMax
	if h.Weekly != nil {
		lo, hi = h.Weekly, h.Weekly
	}
	switch {
	case lo == nil && hi == nil, vh.Min == 0 && vh.Max == 0:
		return na
	case hi != nil && vh.Min > *hi, lo != nil && vh.Max > 0 && *lo > vh.Max:
		return 0
	}
	return 1
}
