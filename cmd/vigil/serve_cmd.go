package main

import (
	"github.com/spf13/cobra"
)

func addServeCommand(root *cobra.Command, inv func(string, string) (Invocation, error), allow *bool) {
	serve := &cobra.Command{
		Use:   "serve",
		Short: "Listen for vigil.summary/v0 over HTTP",
		Long: "Start the HTTP listener that serves vigil.summary/v0 and related\n" +
			"read routes. The bind address comes from the config listen field.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			i, err := inv("", "")
			if err != nil {
				return err
			}
			return runServe(cmd.Context(), i, *allow)
		},
	}
	serve.Flags().BoolVar(allow, "allow-nonloopback", false, "permit a listen address that is not loopback")
	root.AddCommand(serve)
}
