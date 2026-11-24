package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
)

var version = "dev"

func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		fmt.Fprintln(stderr, usage())
		return fmt.Errorf("no command given")
	}

	cmd := args[0]
	rest := args[1:]

	switch cmd {
	case "version":
		return cmdVersion(stdout)
	case "serve":
		return cmdServe(ctx, rest, stdout, stderr)
	case "config":
		return cmdConfig(rest, stdout, stderr)
	case "backup":
		return cmdBackup(rest, stdout, stderr)
	case "gen-api":
		return cmdGenAPI(rest, stdout, stderr)
	default:
		fmt.Fprintln(stderr, usage())
		return fmt.Errorf("unknown command: %s", cmd)
	}
}

func cmdVersion(stdout io.Writer) error {
	fmt.Fprintln(stdout, version)
	return nil
}

func cmdConfig(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: sifty config <subcommand>")
		return fmt.Errorf("no config subcommand given")
	}
	switch args[0] {
	case "defaults":
		return cmdConfigDefaults(stdout)
	default:
		fmt.Fprintf(stderr, "unknown config subcommand: %s\n", args[0])
		return fmt.Errorf("unknown config subcommand: %s", args[0])
	}
}

type serveFlags struct {
	dataDir   string
	port      int
	offline   bool
	debug     bool
	replayDir string
}

func parseServeFlags(args []string) (serveFlags, error) {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	f := serveFlags{port: -1}
	var portVal int
	fs.StringVar(&f.dataDir, "data-dir", "", "data folder (default: $SIFTY_DATA_DIR, then ./data if it exists, then the application folder of this user)")
	fs.IntVar(&portVal, "port", 0, "listen port (0 = take a free port; omit = use settings or default 7373)")
	fs.BoolVar(&f.offline, "offline", false, "disable all network requests and model calls")
	fs.BoolVar(&f.debug, "debug", false, "enable debug mode")
	fs.StringVar(&f.replayDir, "replay", "", "answer requests from recorded responses in this folder")

	if err := fs.Parse(args); err != nil {
		return f, err
	}

	fs.Visit(func(flag *flag.Flag) {
		if flag.Name == "port" {
			f.port = portVal
		}
	})

	return f, nil
}

func usage() string {
	return `sifty <command> [flags]

Commands:
  serve          Start the application server
  backup         Write the database, settings and documents to one archive
  config         Manage configuration
  gen-api        Generate api/openapi.yaml from the route registry
  version        Print the version

Run 'sifty serve --help' for serve options.`
}

func resolveDataDir(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	if v := os.Getenv("SIFTY_DATA_DIR"); v != "" {
		return v
	}
	return defaultDataDir()
}

func defaultDataDir() string {
	if st, err := os.Stat("data"); err == nil && st.IsDir() {
		return "./data"
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "./data"
	}
	if runtime.GOOS == "darwin" {
		return filepath.Join(base, "Sifty")
	}
	return filepath.Join(base, "sifty")
}
