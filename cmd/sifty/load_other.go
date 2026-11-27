//go:build !darwin && !linux

package main

func loadAverage() (float64, bool) {
	return 0, false
}
