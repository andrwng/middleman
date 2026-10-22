package mcp

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// wholeIntArg reads an integer argument, rejecting fractional numbers.
func wholeIntArg(args map[string]any, key string) (int, bool) {
	v, ok := args[key].(float64)
	if !ok || v != float64(int(v)) {
		return 0, false
	}
	return int(v), true
}

func prPath(s *Server) string {
	return fmt.Sprintf("/api/v1/repos/%s/%s/pulls/%d",
		url.PathEscape(s.cfg.ReviewOwner), url.PathEscape(s.cfg.ReviewName), s.cfg.ReviewNumber)
}

// reviewRunTools is the tool set Claude gets during a Review with Claude
// run. Nothing here publishes: proposals land as suggestions the
// reviewer must accept.
func reviewRunTools() map[string]toolDef {
	return map[string]toolDef{
		"propose_comment": {
			name: "propose_comment",
			description: "Stage one review finding as a suggestion for the maintainer. " +
				"The anchor must be a line inside this PR's diff: side RIGHT for added or context lines of the new file, LEFT for deleted lines. " +
				"If the anchor is rejected, the error lists the valid ranges.",
			inputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path":       map[string]any{"type": "string", "description": "Repo-relative path of a file changed in the PR."},
					"side":       map[string]any{"type": "string", "enum": []string{"LEFT", "RIGHT"}},
					"line":       map[string]any{"type": "integer", "description": "Line number on that side (end line of a range)."},
					"start_line": map[string]any{"type": "integer", "description": "First line of a multi-line range, same side."},
					"body":       map[string]any{"type": "string", "description": "Exact comment text to post, Markdown, addressed to the PR author."},
					"rationale":  map[string]any{"type": "string", "description": "Your reasoning for the maintainer; not posted."},
				},
				"required": []string{"path", "side", "line", "body", "rationale"},
			},
			call: func(s *Server, args map[string]any) (string, error) {
				path, _ := args["path"].(string)
				side, _ := args["side"].(string)
				body, _ := args["body"].(string)
				rationale, _ := args["rationale"].(string)
				line, okLine := wholeIntArg(args, "line")
				switch {
				case path == "":
					return "", fmt.Errorf("path is required")
				case side != "LEFT" && side != "RIGHT":
					return "", fmt.Errorf("side must be LEFT or RIGHT")
				case !okLine || line < 1:
					return "", fmt.Errorf("line must be a positive integer")
				case body == "":
					return "", fmt.Errorf("body is required")
				}
				req := map[string]any{"path": path, "side": side, "line": line, "body": body, "rationale": rationale}
				if start, ok := wholeIntArg(args, "start_line"); ok {
					req["start_line"] = start
				}
				b, err := json.Marshal(req)
				if err != nil {
					return "", err
				}
				return s.restJSON("POST", fmt.Sprintf("%s/ai-review-runs/%d/proposals", prPath(s), s.cfg.RunID), b)
			},
		},
		"list_drafts": {
			name:        "list_drafts",
			description: "List the maintainer's draft comments and the suggestions already staged on this PR (path, line, side, body, state).",
			inputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
			call: func(s *Server, _ map[string]any) (string, error) {
				return s.restJSON("GET", prPath(s)+"/review-drafts", nil)
			},
		},
		"get_pull": builtinTools()["get_pull"],
	}
}

func draftPath(s *Server) string {
	return prPath(s) + "/review-drafts/" + url.PathEscape(s.cfg.DraftID)
}

// draftChatTools is the tool set Claude gets while discussing one review
// comment with the reviewer. Revisions follow the server's rules: text
// the reviewer already accepted is never overwritten.
func draftChatTools() map[string]toolDef {
	return map[string]toolDef{
		"get_draft": {
			name:        "get_draft",
			description: "Get the review comment under discussion: path, line, side, current body, and state (suggestion or draft).",
			inputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
			call: func(s *Server, _ map[string]any) (string, error) {
				return s.restJSON("GET", draftPath(s), nil)
			},
		},
		"revise_comment": {
			name: "revise_comment",
			description: "Replace the comment's text. Use only when the reviewer asks for a change; " +
				"body is the complete new comment text. If the reviewer already accepted or edited the comment, " +
				"your text is offered to them instead of replacing theirs.",
			inputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"body": map[string]any{"type": "string", "description": "The complete new comment text, Markdown."},
				},
				"required": []string{"body"},
			},
			call: func(s *Server, args map[string]any) (string, error) {
				body, _ := args["body"].(string)
				if strings.TrimSpace(body) == "" {
					return "", fmt.Errorf("body is required")
				}
				b, err := json.Marshal(map[string]any{"body": body})
				if err != nil {
					return "", err
				}
				return s.restJSON("POST", draftPath(s)+"/revisions", b)
			},
		},
		"get_pull": builtinTools()["get_pull"],
	}
}
