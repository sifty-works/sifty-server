package candidate

import (
	"context"
	"fmt"
	"strings"

	"github.com/sifty-works/sifty-server/internal/db"
)

func EvidenceSignature(ctx context.Context, database *db.DB) (string, error) {
	rows, err := selectEvidenceRows(ctx, database)
	if err != nil {
		return "", err
	}
	var parts []string
	for _, r := range rows {
		parts = append(parts, fmt.Sprintf("%d:%s:%d", r.id, r.state, r.chars))
	}
	return strings.Join(parts, ","), nil
}

func join(sep string, parts ...string) string {
	var kept []string
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, sep)
}

func bracket(s string) string {
	if s = strings.TrimSpace(s); s == "" {
		return ""
	}
	return "(" + s + ")"
}

func labelled(label string, values ...string) []string {
	var out []string
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, label+": "+v)
		}
	}
	return out
}

func EvidenceText(me MeData, p ProfileData, documents []string) string {
	var sb strings.Builder
	section := func(title string, lines ...string) {
		if len(lines) == 0 {
			return
		}
		sb.WriteString(title + "\n")
		for _, l := range lines {
			sb.WriteString("- " + l + "\n")
		}
		sb.WriteString("\n")
	}
	var skills, absent, facts, jobs, langs, auths, where, wants []string
	for _, s := range me.Skills {
		skills = append(skills, join(" ", s.Name, bracket(s.Level)))
	}
	for _, s := range me.AbsentSkills {
		absent = append(absent, s.Name)
	}
	for _, f := range me.ConfirmedFacts {
		if f.Have {
			facts = append(facts, "Has: "+f.Text)
		} else {
			facts = append(facts, "Does not have: "+f.Text)
		}
	}
	for _, e := range me.Experience {
		end := e.End
		if e.Current {
			end = "present"
		}
		jobs = append(jobs, join("; ", join(" ", join(" at ", e.Title, e.Company), bracket(join(" - ", e.Start, end))), e.Summary, join(", ", e.Skills...)))
	}
	for _, l := range me.Languages {
		langs = append(langs, join(" ", l.Name, bracket(l.Level)))
	}
	for _, a := range me.WorkAuthorizations {
		auths = append(auths, join(": ", a.Country, a.Status))
	}
	if l := me.CurrentLocation; l != nil {
		where = append(where, join(", ", l.City, l.Country))
	}
	section("SKILLS", skills...)
	section("SKILLS THE CANDIDATE DOES NOT HAVE", absent...)
	section("CONFIRMED BY THE CANDIDATE", facts...)
	section("EXPERIENCE", jobs...)
	section("LANGUAGES", langs...)
	section("WORK AUTHORIZATIONS", auths...)
	section("CURRENT LOCATION", where...)
	if a := me.Availability; a != nil {
		notice := ""
		if a.NoticeValue != nil {
			notice = fmt.Sprintf("notice %d %s", *a.NoticeValue, a.NoticeUnit)
		}
		section("AVAILABILITY", join("; ", notice, a.StartDate))
	}
	wants = append(wants, labelled("headline", p.Headline)...)
	wants = append(wants, labelled("target role", p.TargetRoles...)...)
	wants = append(wants, labelled("engagement", p.Engagement...)...)
	wants = append(wants, labelled("work mode", p.WorkModes...)...)
	wants = append(wants, labelled("time zone", p.TimeZones...)...)
	for _, l := range p.PreferredLocations {
		wants = append(wants, labelled("preferred location", join(", ", l.City, l.Country))...)
	}
	wants = append(wants, labelled("relocation", p.Relocation)...)
	wants = append(wants, labelled("other requirements", p.OtherRequirements)...)
	section("WHAT THE CANDIDATE LOOKS FOR", wants...)
	for i, text := range documents {
		fmt.Fprintf(&sb, "DOCUMENT %d\n%s\n\n", i+1, strings.TrimSpace(text))
	}
	return strings.TrimSpace(sb.String())
}
