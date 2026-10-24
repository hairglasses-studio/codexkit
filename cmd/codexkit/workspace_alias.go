package main

import (
	"context"
	"errors"
	"flag"
	"io"

	"github.com/hairglasses-studio/codexkit/workspace"
)

type aliasNames []string

func (names *aliasNames) String() string         { return "" }
func (names *aliasNames) Set(value string) error { *names = append(*names, value); return nil }

func runWorkspaceAliasReconcile(args []string) error {
	flags := flag.NewFlagSet("workspace reconcile-aliases", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var request workspace.AliasReconcileRequest
	var names aliasNames
	var jsonOut bool
	flags.StringVar(&request.Root, "root", "", "canonical workspace root")
	flags.Var(&names, "alias", "exact active symlink alias row; repeatable")
	flags.StringVar(&request.ExpectedSHA256, "expected-sha256", "", "reviewed manifest SHA256 required for apply")
	flags.BoolVar(&request.Apply, "apply", false, "apply the exact proven row removals")
	flags.BoolVar(&jsonOut, "json", false, "JSON output (always enabled)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	request.Aliases = names
	report, err := workspace.ReconcileAliases(context.Background(), request)
	if report.Schema != "" {
		printJSON(report)
	}
	return err
}
