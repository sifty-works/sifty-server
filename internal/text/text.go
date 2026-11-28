package text

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

type folded struct {
	str     string
	offsets []int
}

func normRune(r rune) rune {
	switch r {
	case '‘', '’', '‚', '‛', '′', 'ʼ', '`', '´':
		return '\''
	case '“', '”', '„', '‟', '″', '«', '»':
		return '"'
	case '‐', '‑', '‒', '–', '—', '―', '−':
		return '-'
	case '\u200B', '\u200C', '\u200D', '\u2060', '\uFEFF', '\u00AD':
		return 0
	}
	if unicode.IsSpace(r) {
		return ' '
	}
	return r
}

func Fold(s string) string { return fold(s).str }

func Key(s string) string {
	var b strings.Builder
	space := true
	for _, r := range strings.ToLower(Fold(s)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			space = false
		} else if !space {
			b.WriteByte(' ')
			space = true
		}
	}
	return strings.TrimRight(b.String(), " ")
}

func fold(text string) folded {
	var sb strings.Builder
	var offsets []int
	prevSpace := true
	for i, r := range text {
		n := normRune(r)
		if n == 0 {
			continue
		}
		if n == ' ' {
			if prevSpace {
				continue
			}
			prevSpace = true
		} else {
			prevSpace = false
		}
		nb, _ := sb.WriteRune(n)
		for j := 0; j < nb; j++ {
			offsets = append(offsets, i)
		}
	}
	s := sb.String()
	for len(s) > 0 && s[len(s)-1] == ' ' {
		s = s[:len(s)-1]
		offsets = offsets[:len(offsets)-1]
	}
	return folded{str: s, offsets: offsets}
}

func LocateQuote(source, quote string) (start, end int, ok bool) {
	needle := fold(quote)
	if utf8.RuneCountInString(needle.str) < 3 {
		return 0, 0, false
	}
	hay := fold(source)
	at := strings.Index(hay.str, needle.str)
	if at < 0 {
		return 0, 0, false
	}
	last := hay.offsets[at+len(needle.str)-1]
	_, sz := utf8.DecodeRuneInString(source[last:])
	return hay.offsets[at], last + sz, true
}
