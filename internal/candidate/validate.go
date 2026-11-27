package candidate

import (
	"fmt"
	"strings"
)

func ValidateMeData(me MeData) []string {
	var errs []string
	errs = append(errs, validateSkills(me.Skills, me.AbsentSkills)...)
	errs = append(errs, validateLanguages(me.Languages)...)
	errs = append(errs, validateAvailability(me.Availability)...)
	errs = append(errs, validateCurrentLocation(me.CurrentLocation)...)
	errs = append(errs, validateWorkAuthorizations(me.WorkAuthorizations)...)
	return errs
}

func ValidateProfileData(p ProfileData) []string {
	var errs []string
	if len([]rune(p.Headline)) > 160 {
		errs = append(errs, "Headline must be at most 160 characters.")
	}
	if strings.ContainsAny(p.Headline, "\n\r") {
		errs = append(errs, "Headline must be a single line.")
	}
	errs = append(errs, validateCompensation(p.Compensation)...)
	errs = append(errs, validateOptions("Engagement", "Employment, Contract or Internship", p.EngagementType, "employment", "contract", "internship")...)
	errs = append(errs, validateOptions("Working time", "Full-time or Part-time", p.WorkingTime, "full_time", "part_time")...)
	errs = append(errs, validateHours(p.Hours)...)
	errs = append(errs, validateOptions("Work mode", "Remote, Hybrid or Onsite", p.WorkMode, "remote", "hybrid", "onsite")...)
	errs = append(errs, validatePlaces(p.Locations)...)
	return errs
}

func validateSkills(skills []Skill, absent []AbsentSkill) []string {
	var errs []string
	absentNames := make(map[string]bool)
	for _, a := range absent {
		absentNames[strings.ToLower(strings.TrimSpace(a.Name))] = true
	}
	seen := make(map[string]bool)
	for i, s := range skills {
		name := strings.TrimSpace(s.Name)
		if name == "" {
			errs = append(errs, fmt.Sprintf("Skill %d: enter a name.", i+1))
			continue
		}
		key := strings.ToLower(name)
		if seen[key] {
			errs = append(errs, fmt.Sprintf("%q appears more than once in your skills.", name))
		}
		seen[key] = true
		if absentNames[key] {
			errs = append(errs, fmt.Sprintf("%q is both in your skills and among the skills you do not have.", name))
		}
	}
	return errs
}

func validateLanguages(langs []Language) []string {
	var errs []string
	for i, l := range langs {
		if strings.TrimSpace(l.Name) == "" {
			errs = append(errs, fmt.Sprintf("Language %d: choose a language.", i+1))
		}
	}
	return errs
}

func validateAvailability(a *Availability) []string {
	if a == nil {
		return nil
	}
	var errs []string
	if a.NoticeValue != nil {
		if *a.NoticeValue <= 0 {
			errs = append(errs, "Notice period: enter a number greater than zero.")
		}
		if a.NoticeUnit == "" {
			errs = append(errs, "Notice period: choose days, weeks or months.")
		} else if a.NoticeUnit != "day" && a.NoticeUnit != "week" && a.NoticeUnit != "month" {
			errs = append(errs, "Notice period: choose days, weeks or months.")
		}
	}
	return errs
}

func validateCurrentLocation(loc *Location) []string {
	if loc == nil {
		return nil
	}
	if strings.TrimSpace(loc.Country) == "" && strings.TrimSpace(loc.City) != "" {
		return []string{"Current location: choose a country."}
	}
	return nil
}

func validateWorkAuthorizations(auths []WorkAuthorization) []string {
	var errs []string
	for i, a := range auths {
		if strings.TrimSpace(a.Country) == "" {
			errs = append(errs, fmt.Sprintf("Work authorization %d: choose a country.", i+1))
		}
		if a.Status != "" && a.Status != "eligible" && a.Status != "need_visa" {
			errs = append(errs, fmt.Sprintf("Work authorization %d: choose Eligible to work, Need visa sponsorship or Not set.", i+1))
		}
	}
	return errs
}

func validateCompensation(c *Compensation) []string {
	if c == nil {
		return nil
	}
	var errs []string
	hasAmount := (c.CashMin != nil && *c.CashMin != 0) || (c.CashTarget != nil && *c.CashTarget != 0)
	if hasAmount && strings.TrimSpace(c.Currency) == "" {
		errs = append(errs, "Compensation: choose a currency for the amount.")
	}
	if c.CashMin != nil && c.CashTarget != nil && *c.CashTarget < *c.CashMin {
		errs = append(errs, "Compensation: the desired amount must be at least the minimum.")
	}
	if c.GrossNet != "" && c.GrossNet != "gross" && c.GrossNet != "net" {
		errs = append(errs, "Compensation: choose Before taxes or After taxes.")
	}
	if c.Period != "" && c.Period != "month" && c.Period != "year" {
		errs = append(errs, "Compensation: choose Monthly or Yearly.")
	}
	if c.Crypto != "" && c.Crypto != "fiat_only" && c.Crypto != "crypto_accepted" && c.Crypto != "crypto_only" {
		errs = append(errs, "Compensation: choose a payment method.")
	}
	if c.DayRates != nil {
		hasDayAmount := (c.DayRates.Min != nil && *c.DayRates.Min != 0) || (c.DayRates.Target != nil && *c.DayRates.Target != 0)
		if hasDayAmount && strings.TrimSpace(c.DayRates.Currency) == "" {
			errs = append(errs, "Day rate: choose a currency.")
		}
		if c.DayRates.Min != nil && c.DayRates.Target != nil && *c.DayRates.Target < *c.DayRates.Min {
			errs = append(errs, "Day rate: the desired amount must be at least the minimum.")
		}
	}
	return errs
}

func validateHours(h *Hours) []string {
	if h == nil {
		return nil
	}
	var errs []string
	if h.WeeklyMin != nil && h.WeeklyMax != nil && *h.WeeklyMax < *h.WeeklyMin {
		errs = append(errs, "Hours per week: the maximum must be at least the minimum.")
	}
	return errs
}
