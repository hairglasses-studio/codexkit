package unificationaudit

import (
	"fmt"

	"github.com/hairglasses-studio/codexkit"
	"github.com/hairglasses-studio/codexkit/workspace"
)

type module struct{}

// Module returns a ToolModule exposing the codebase unification audit.
func Module() codexkit.ToolModule { return &module{} }

func (m *module) Name() string { return "unificationaudit" }
func (m *module) Init() error  { return nil }

func (m *module) Tools() []codexkit.ToolDef {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"root": map[string]any{
				"type":        "string",
				"description": "Workspace root. Defaults to ~/hairglasses-studio.",
			},
			"all_scopes": map[string]any{
				"type":        "boolean",
				"description": "Include inactive and compatibility-only repos.",
			},
		},
	}
	cycleSchema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"root": map[string]any{
				"type":        "string",
				"description": "Workspace root. Defaults to ~/hairglasses-studio.",
			},
			"all_scopes": map[string]any{
				"type":        "boolean",
				"description": "Include inactive and compatibility-only repos.",
			},
			"notes_dir": map[string]any{
				"type":        "string",
				"description": "Directory containing unification cycle notes.",
			},
			"previous_notes": map[string]any{
				"type":        "string",
				"description": "Previous cycle note to read for carry-forward improvement notes. Defaults to the latest note in notes_dir.",
			},
			"cycle_id": map[string]any{
				"type":        "string",
				"description": "Cycle id used in the generated note title.",
			},
			"require_notes_applied": map[string]any{
				"type":        "boolean",
				"description": "Return an error when previous cycle notes have unchecked carry-forward items.",
			},
			"ack_carry_forward": map[string]any{
				"type":        "string",
				"description": "Reason to acknowledge unchecked carry-forward items while still generating a cycle note.",
			},
		},
	}
	shellFileSchema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"repo_path": map[string]any{
				"type":        "string",
				"description": "Repo root containing the selected shell file.",
			},
			"path": map[string]any{
				"type":        "string",
				"description": "Repo-relative shell file path to inventory.",
			},
			"markdown": map[string]any{
				"type":        "boolean",
				"description": "Return Markdown instead of structured JSON.",
			},
		},
		"required": []string{"repo_path", "path"},
	}
	return []codexkit.ToolDef{
		{
			Name:        "unification_audit",
			Description: "Scan the workspace for shell migration, hook consolidation, and skill-source unification candidates.",
			Annotations: codexkit.ToolAnnotations(true, false, true, false),
			Schema:      schema,
			Handler: func(params map[string]any) (any, error) {
				root, allScopes := paramsRootAndScope(params)
				report, err := Audit(root, Options{AllScopes: allScopes})
				if err != nil {
					return nil, fmt.Errorf("run unification audit: %w", err)
				}
				return report, nil
			},
		},
		{
			Name:        "unification_report",
			Description: "Return the codebase unification audit as Markdown.",
			Annotations: codexkit.ToolAnnotations(true, false, true, false),
			Schema:      schema,
			Handler: func(params map[string]any) (any, error) {
				root, allScopes := paramsRootAndScope(params)
				report, err := Audit(root, Options{AllScopes: allScopes})
				if err != nil {
					return nil, fmt.Errorf("run unification audit: %w", err)
				}
				return report.Markdown(), nil
			},
		},
		{
			Name:        "unification_cycle",
			Description: "Generate a repeatable unification cycle note from the live audit and previous-cycle improvement notes.",
			Annotations: codexkit.ToolAnnotations(true, false, true, false),
			Schema:      cycleSchema,
			Handler: func(params map[string]any) (any, error) {
				root, allScopes := paramsRootAndScope(params)
				notesDir, _ := params["notes_dir"].(string)
				previousNotes, _ := params["previous_notes"].(string)
				cycleID, _ := params["cycle_id"].(string)
				requireNotesApplied, _ := params["require_notes_applied"].(bool)
				ackCarryForward, _ := params["ack_carry_forward"].(string)
				cycle, err := BuildCycle(root, CycleOptions{
					AllScopes:                   allScopes,
					NotesDir:                    notesDir,
					PreviousNotes:               previousNotes,
					CycleID:                     cycleID,
					RequireNotesApplied:         requireNotesApplied,
					CarryForwardAcknowledgement: ackCarryForward,
				})
				if err != nil {
					return nil, fmt.Errorf("build unification cycle: %w", err)
				}
				return cycle.Markdown(), nil
			},
		},
		{
			Name:        "unification_shell_file",
			Description: "Inventory functions, command entrypoints, and obvious callers for one shell file before a migration slice.",
			Annotations: codexkit.ToolAnnotations(true, false, true, false),
			Schema:      shellFileSchema,
			Handler: func(params map[string]any) (any, error) {
				repoPath, _ := params["repo_path"].(string)
				path, _ := params["path"].(string)
				inventory, err := InventoryShellFile(repoPath, path)
				if err != nil {
					return nil, fmt.Errorf("inventory shell file: %w", err)
				}
				markdown, _ := params["markdown"].(bool)
				if markdown {
					return inventory.Markdown(), nil
				}
				return inventory, nil
			},
		},
	}
}

func paramsRootAndScope(params map[string]any) (string, bool) {
	root, _ := params["root"].(string)
	if root == "" {
		root = workspace.DefaultRoot()
	}
	allScopes, _ := params["all_scopes"].(bool)
	return root, allScopes
}
