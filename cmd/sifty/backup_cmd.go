package main

import (
	"archive/tar"
	"compress/gzip"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sifty-works/sifty-server/internal/db"
)

const (
	settingsFileName = "sifty.yaml"
	filesDirName     = "files"
)

var sitesFileName = filepath.Join("vocab", "sites.yaml")

func cmdBackup(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("backup", flag.ContinueOnError)
	flags.SetOutput(stderr)

	var dataFlag, out string
	flags.StringVar(&dataFlag, "data-dir", "", "data folder (default: $SIFTY_DATA_DIR, then ./data if it exists, then the application folder of this user)")
	flags.StringVar(&out, "out", "", "archive to write (default: sifty-backup-<UTC time>.tar.gz in the working folder)")
	if err := flags.Parse(args); err != nil {
		return err
	}

	dataDir, err := filepath.Abs(resolveDataDir(dataFlag))
	if err != nil {
		return fmt.Errorf("resolving data dir: %w", err)
	}
	if out == "" {
		out = "sifty-backup-" + time.Now().UTC().Format("20060102T150405Z") + ".tar.gz"
	}
	out, err = filepath.Abs(out)
	if err != nil {
		return fmt.Errorf("resolving --out: %w", err)
	}

	size, err := writeBackup(dataDir, out)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return err
	}

	if left := leftOut(dataDir); len(left) > 0 {
		fmt.Fprintf(stdout, "Left out: %s.\n", strings.Join(left, ", "))
	}
	fmt.Fprintf(stdout, "%s (%d bytes)\n", out, size)
	return nil
}

func writeBackup(dataDir, out string) (int64, error) {
	dbPath := filepath.Join(dataDir, dbFileName)
	if st, err := os.Stat(dbPath); err != nil || !st.Mode().IsRegular() {
		return 0, fmt.Errorf("There is no %s in the data folder %s.", dbFileName, dataDir)
	}

	file, err := os.OpenFile(out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		if os.IsExist(err) {
			return 0, fmt.Errorf("The file %s already exists; nothing was written.", out)
		}
		return 0, fmt.Errorf("Cannot write %s: %w", out, err)
	}
	size, err := fillArchive(file, dataDir)
	if cerr := file.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(out)
		return 0, fmt.Errorf("Backup of %s failed: %w", dataDir, err)
	}
	return size, nil
}

func fillArchive(file *os.File, dataDir string) (int64, error) {
	tmp, err := os.MkdirTemp("", "sifty-backup-")
	if err != nil {
		return 0, err
	}
	defer os.RemoveAll(tmp)

	dbCopy := filepath.Join(tmp, dbFileName)
	if err := db.CopyReadOnly(filepath.Join(dataDir, dbFileName), dbCopy); err != nil {
		return 0, err
	}

	gz := gzip.NewWriter(file)
	tw := tar.NewWriter(gz)

	if err := addFile(tw, dbCopy, dbFileName); err != nil {
		return 0, err
	}
	settings := filepath.Join(dataDir, settingsFileName)
	if st, err := os.Stat(settings); err == nil && st.Mode().IsRegular() {
		if err := addFile(tw, settings, settingsFileName); err != nil {
			return 0, err
		}
	}
	sites := filepath.Join(dataDir, sitesFileName)
	if st, err := os.Stat(sites); err == nil && st.Mode().IsRegular() {
		if err := addFile(tw, sites, filepath.ToSlash(sitesFileName)); err != nil {
			return 0, err
		}
	}
	filesDir := filepath.Join(dataDir, filesDirName)
	if st, err := os.Stat(filesDir); err == nil && st.IsDir() {
		err := filepath.WalkDir(filesDir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(dataDir, path)
			if err != nil {
				return err
			}
			switch {
			case d.IsDir():
				return addDir(tw, path, filepath.ToSlash(rel))
			case d.Type().IsRegular():
				return addFile(tw, path, filepath.ToSlash(rel))
			}
			return nil
		})
		if err != nil {
			return 0, err
		}
	}

	if err := tw.Close(); err != nil {
		return 0, err
	}
	if err := gz.Close(); err != nil {
		return 0, err
	}
	st, err := file.Stat()
	if err != nil {
		return 0, err
	}
	return st.Size(), nil
}

func addDir(tw *tar.Writer, path, name string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	hdr, err := tar.FileInfoHeader(info, "")
	if err != nil {
		return err
	}
	hdr.Name = name + "/"
	return tw.WriteHeader(hdr)
}

func addFile(tw *tar.Writer, path, name string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	hdr, err := tar.FileInfoHeader(info, "")
	if err != nil {
		return err
	}
	hdr.Name = name
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	_, err = io.Copy(tw, f)
	return err
}

func leftOut(dataDir string) []string {
	entries, err := os.ReadDir(dataDir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		switch e.Name() {
		case dbFileName, settingsFileName, filesDirName:
			continue
		case "vocab":
			if others, _ := os.ReadDir(filepath.Join(dataDir, "vocab")); len(others) == 1 && others[0].Name() == "sites.yaml" {
				continue
			}
		}
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out
}
