package main

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
)

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version info",
		Run: func(_ *cobra.Command, _ []string) {
			fmt.Printf(`fleet-plan %s
Built: %s
Go:    %s
OS:    %s/%s
`, version, buildDate, goVersion, runtime.GOOS, runtime.GOARCH)
		},
	}
}
