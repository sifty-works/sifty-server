package models

import "time"

func (g *Gate) SetClock(now func() time.Time) { g.now = now }
