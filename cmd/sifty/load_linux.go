//go:build linux

package main

import (
	"os"
	"strconv"
	"strings"
)

func loadAverage() (float64, bool) {
	raw, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0, false
	}
	v, err := strconv.ParseFloat(strings.Fields(string(raw))[0], 64)
	return v, err == nil
}
