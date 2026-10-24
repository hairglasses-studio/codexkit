package main

import "testing"

func TestAliasCLIRequiresExplicitScopeAndTypedFlags(t *testing.T) {
	for _, args := range [][]string{{}, {"--unknown"}, {"--root", "/tmp", "--alias", "*"}, {"--root", "/tmp", "--alias", "staging", "--apply"}, {"--apply=maybe"}, {"unexpected-positional"}} {
		if err := runWorkspaceAliasReconcile(args); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}
