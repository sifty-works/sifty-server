package config

import (
	"os"
	"path/filepath"
	"time"
)

func LoadOverride(path string, apply func([]byte) error) (broken string, err error) {
	data, readErr := os.ReadFile(path)
	if os.IsNotExist(readErr) {
		return "", nil
	}
	if readErr != nil {
		return "", readErr
	}
	err = apply(data)
	if err != nil {
		if b := path + ".broken-" + time.Now().UTC().Format("20060102T150405"); os.Rename(path, b) == nil {
			broken = filepath.Base(b)
		}
	}
	return broken, err
}
