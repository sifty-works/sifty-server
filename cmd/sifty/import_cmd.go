package main

import (
	"flag"
	"fmt"
	"io"
	"path/filepath"

	"github.com/sifty-works/sifty-server/internal/importv1"
	"golang.org/x/net/context"
)

func cmdImportV1(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("import-v1", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var fromDir, dataDir string
	fs.StringVar(&fromDir, "from", "", "old data folder containing store.sqlite")
	fs.StringVar(&dataDir, "data-dir", "", "new empty data folder to create")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if fromDir == "" {
		fmt.Fprintln(stderr, "import-v1: --from is required")
		return fmt.Errorf("--from is required")
	}
	if dataDir == "" {
		fmt.Fprintln(stderr, "import-v1: --data-dir is required")
		return fmt.Errorf("--data-dir is required")
	}

	absFrom, err := filepath.Abs(fromDir)
	if err != nil {
		return fmt.Errorf("resolving --from: %w", err)
	}
	absData, err := filepath.Abs(dataDir)
	if err != nil {
		return fmt.Errorf("resolving --data-dir: %w", err)
	}

	dbPath := filepath.Join(absData, dbFileName)
	settingsPath := filepath.Join(absData, settingsFileName)

	if err := importv1.Import(ctx, absFrom, dbPath, settingsPath, stdout); err != nil {
		fmt.Fprintln(stderr, err)
		return err
	}
	return nil
}
