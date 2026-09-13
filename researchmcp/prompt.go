package researchmcp

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	researchWorkflowPrompt = "research-workflow"
	maxQuestionBytes       = 1024
)

func (adapter *adapter) registerPrompt(server *mcp.Server) {
	server.AddPrompt(&mcp.Prompt{
		Name:        researchWorkflowPrompt,
		Title:       "Offline research workflow",
		Description: "Plan a concise, evidence-led workflow over the offline research gateway.",
		Arguments: []*mcp.PromptArgument{{
			Name:        "question",
			Description: "The bounded research question to investigate.",
			Required:    true,
		}},
	}, adapter.workflowPrompt)
}

func (adapter *adapter) workflowPrompt(ctx context.Context, request *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	question := strings.TrimSpace(request.Params.Arguments["question"])
	if question == "" || !utf8.ValidString(question) || strings.ContainsRune(question, '\x00') || len(question) > maxQuestionBytes {
		return nil, fmt.Errorf("question must be 1-%d UTF-8 bytes", maxQuestionBytes)
	}
	gateway, err := adapter.open(ctx)
	if err != nil {
		return nil, err
	}
	overview := gateway.Overview()
	body := fmt.Sprintf("Investigate this question using only the offline research MCP surfaces: %s\n\nStart with research_catalog, then research_search. Use research_detail on relevant IDs and research_read only for document chunks, following next_cursor until sufficient. Distinguish current freshness from captured facts, cite document IDs or record URLs, and state gaps without fetching the network. Snapshot %s contains %d documents and %d typed records.",
		question, overview.SnapshotID, overview.Summary.Documents, overview.Summary.Records)
	return &mcp.GetPromptResult{
		Description: "Offline evidence workflow for the supplied research question.",
		Messages: []*mcp.PromptMessage{{
			Role:    mcp.Role("user"),
			Content: &mcp.TextContent{Text: body},
		}},
	}, nil
}
