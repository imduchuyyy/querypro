package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"querypro/internal/mcp"
	"querypro/internal/plugin"
	"querypro/internal/store"
	"querypro/internal/tui"
)

var version = "dev"

func main() {
	if err := rootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "querypro:", err)
		os.Exit(1)
	}
}

func rootCmd() *cobra.Command {
	var plugins string
	open := func() (*store.Store, error) {
		home, err := store.Dir()
		if err != nil {
			return nil, err
		}
		return store.Open(home)
	}
	withHost := func(fn func(*plugin.Host, *store.Store) error) error {
		st, err := open()
		if err != nil {
			return err
		}
		host, err := plugin.NewHost(pluginDir(plugins), st.Path("logs"))
		if err != nil {
			return err
		}
		defer host.Close()
		return fn(host, st)
	}
	root := &cobra.Command{
		Use:           "querypro",
		Short:         "A terminal UI for many datastores",
		Version:       version,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(*cobra.Command, []string) error {
			return withHost(func(h *plugin.Host, st *store.Store) error {
				return tui.Run(h, st)
			})
		},
	}
	root.SetVersionTemplate("{{.Version}}\n")
	root.PersistentFlags().StringVar(&plugins, "plugins", "",
		"plugin directory (default: $QUERYPRO_PLUGINS or plugins next to the binary)")
	root.AddCommand(
		&cobra.Command{
			Use:   "mcp",
			Short: "Serve saved connections to AI clients over MCP stdio",
			Args:  cobra.NoArgs,
			RunE: func(*cobra.Command, []string) error {
				return withHost(func(h *plugin.Host, st *store.Store) error {
					return mcp.Serve(context.Background(), h, st, version)
				})
			},
		},
		connCmd(open, func() ([]plugin.Kind, error) {
			return plugin.Discover(pluginDir(plugins))
		}),
		queryCmd(withHost),
	)
	return root
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
