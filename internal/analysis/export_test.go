package analysis

import "github.com/sifty-works/sifty-server/internal/facts"

func Effective(all []facts.Fact) []facts.Fact { return effective(all) }
