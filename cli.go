package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"querypro/internal/plugin"
	"querypro/internal/store"
)

func connCmd(open func() (*store.Store, error), kinds func() ([]plugin.Kind, error)) *cobra.Command {
	profiles := func() (*store.Store, []store.Profile, error) {
		st, err := open()
		if err != nil {
			return nil, nil, err
		}
		var ps []store.Profile
		return st, ps, st.Load(store.ProfilesFile, &ps)
	}
	conn := &cobra.Command{Use: "conn", Short: "Manage saved connections"}
	conn.AddCommand(
		&cobra.Command{
			Use:     "list",
			Aliases: []string{"ls"},
			Short:   "List saved connections",
			Args:    cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				_, ps, err := profiles()
				if err != nil {
					return err
				}
				w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
				for _, p := range ps {
					if _, err := fmt.Fprintf(w, "%s\t%s\n", p.Name, p.Kind); err != nil {
						return err
					}
				}
				return w.Flush()
			},
		},
		&cobra.Command{
			Use:   "add <name> <kind> <uri>",
			Short: "Save a connection",
			Args:  cobra.ExactArgs(3),
			RunE: func(_ *cobra.Command, args []string) error {
				p := store.Profile{Name: args[0], Kind: args[1], URI: args[2]}
				ks, err := kinds()
				if err != nil {
					return err
				}
				var names []string
				for _, k := range ks {
					names = append(names, k.Name)
				}
				if !slices.Contains(names, p.Kind) {
					return fmt.Errorf("unknown kind %q, use one of: %s", p.Kind, strings.Join(names, ", "))
				}
				st, ps, err := profiles()
				if err != nil {
					return err
				}
				if err := store.CheckName(ps, p.Name, ""); err != nil {
					return err
				}
				return st.Save(store.ProfilesFile, append(ps, p))
			},
		},
		&cobra.Command{
			Use:     "rm <name>",
			Aliases: []string{"remove"},
			Short:   "Delete a saved connection",
			Args:    cobra.ExactArgs(1),
			RunE: func(_ *cobra.Command, args []string) error {
				st, ps, err := profiles()
				if err != nil {
					return err
				}
				i := slices.IndexFunc(ps, func(p store.Profile) bool { return p.Name == args[0] })
				if i < 0 {
					return fmt.Errorf("no saved connection named %q", args[0])
				}
				return st.Save(store.ProfilesFile, slices.Delete(ps, i, i+1))
			},
		},
	)
	return conn
}

func queryCmd(withHost func(func(*plugin.Host, *store.Store) error) error) *cobra.Command {
	return &cobra.Command{
		Use:   "query <connection> <query>...",
		Short: "Run a query on a saved connection; live streams run until ctrl+c",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
			defer stop()
			return withHost(func(h *plugin.Host, st *store.Store) error {
				var ps []store.Profile
				if err := st.Load(store.ProfilesFile, &ps); err != nil {
					return err
				}
				i := slices.IndexFunc(ps, func(p store.Profile) bool { return p.Name == args[0] })
				if i < 0 {
					return fmt.Errorf("no saved connection named %q", args[0])
				}
				sess, err := h.Connect(ctx, ps[i].Kind, ps[i].URI)
				if err != nil {
					return fmt.Errorf("connect %s: %w", args[0], err)
				}
				defer func() { _ = sess.Close() }()
				return runQuery(ctx, cmd.OutOrStdout(), cmd.ErrOrStderr(), sess, strings.Join(args[1:], " "))
			})
		},
	}
}

var oneLine = strings.NewReplacer("\t", " ", "\r\n", "↵", "\n", "↵")

func runQuery(ctx context.Context, out, info io.Writer, sess plugin.Session, q string) error {
	res, err := sess.Query(ctx, q)
	if err != nil {
		return err
	}
	if res.Summary != "" {
		_, _ = fmt.Fprintln(info, res.Summary)
	}
	if res.Stream != nil {
		for line := range res.Stream {
			if _, err := fmt.Fprintln(out, line); err != nil {
				return err
			}
		}
		return res.Err()
	}
	if res.Columns == nil {
		_, err := io.WriteString(out, res.Text)
		if err == nil && res.Text != "" && !strings.HasSuffix(res.Text, "\n") {
			_, err = io.WriteString(out, "\n")
		}
		return err
	}
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	for _, row := range append([][]string{res.Columns}, res.Rows...) {
		cells := make([]string, len(row))
		for i, cell := range row {
			cells[i] = oneLine.Replace(cell)
		}
		if _, err := fmt.Fprintln(w, strings.Join(cells, "\t")); err != nil {
			return err
		}
	}
	return w.Flush()
}
