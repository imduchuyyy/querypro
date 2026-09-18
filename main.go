package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"querypro/internal/plugin"
	"querypro/internal/store"
	"querypro/internal/tui"
)

var version = "dev"

func main() {
	plugins := flag.String("plugins", "", "plugin directory (default: $QUERYPRO_PLUGINS or plugins next to the binary)")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return
	}
	if err := run(pluginDir(*plugins)); err != nil {
		fmt.Fprintln(os.Stderr, "querypro:", err)
		os.Exit(1)
	}
}

func run(plugins string) error {
	home, err := store.Dir()
	if err != nil {
		return err
	}
	st, err := store.Open(home)
	if err != nil {
		return err
	}
	host, err := plugin.NewHost(plugins, filepath.Join(home, "logs"))
	if err != nil {
		return err
	}
	defer host.Close()
	return tui.Run(host, st)
}

func pluginDir(flagged string) string {
	if flagged != "" {
		return flagged
	}
	if d := os.Getenv("QUERYPRO_PLUGINS"); d != "" {
		return d
	}
	if exe, err := os.Executable(); err == nil {
		if exe, err = filepath.EvalSymlinks(exe); err == nil {
			dir := filepath.Dir(exe)
			for _, d := range []string{filepath.Join(dir, "plugins"), filepath.Join(dir, "..", "plugins")} {
				if _, err := os.Stat(filepath.Join(d, "sdk")); err == nil {
					return d
				}
			}
		}
	}
	return "plugins"
}
