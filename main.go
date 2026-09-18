package main

import (
	"flag"
	"fmt"
	"os"

	"querypro/internal/tui"
)

func main() {
	demo := flag.Bool("demo", false, "start with mock connections for every backend")
	flag.Parse()
	if err := tui.Run(*demo); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
