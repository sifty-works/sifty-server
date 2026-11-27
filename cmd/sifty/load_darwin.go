//go:build darwin

package main

import (
	"encoding/binary"

	"golang.org/x/sys/unix"
)

func loadAverage() (float64, bool) {
	raw, err := unix.SysctlRaw("vm.loadavg")
	if err != nil || len(raw) < 24 {
		return 0, false
	}
	scale := binary.NativeEndian.Uint64(raw[16:24])
	if scale == 0 {
		return 0, false
	}
	return float64(binary.NativeEndian.Uint32(raw[0:4])) / float64(scale), true
}
