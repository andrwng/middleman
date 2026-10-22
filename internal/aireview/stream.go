package aireview

import (
	"encoding/json"
	"strings"
)

// streamEvent is one normalized step from claude's stream-json output.
// The frontend renders these as cards (tool calls + results) plus
// text bubbles, in addition to the final summary stored on
// turn.content.
type streamEvent struct {
	Type      string          `json:"type"` // "text" | "tool_use" | "tool_result"
	Text      string          `json:"text,omitempty"`
	Tool      string          `json:"tool,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ID        string          `json:"id,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   string          `json:"content,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
}

// streamState is what we serialize into the turn's raw_json column.
// Kept compact so the polling endpoint stays cheap.
type streamState struct {
	SessionID string        `json:"session_id,omitempty"`
	Events    []streamEvent `json:"events"`
}

// streamMessage is the wire shape claude emits as JSON-lines. We
// pick out only the fields we care about; unknown fields are
// ignored.
type streamMessage struct {
	Type      string              `json:"type"`
	Subtype   string              `json:"subtype,omitempty"`
	SessionID string              `json:"session_id,omitempty"`
	Message   *streamInnerMessage `json:"message,omitempty"`
	Result    string              `json:"result,omitempty"`
	IsError   bool                `json:"is_error,omitempty"`
}

type streamInnerMessage struct {
	Role    string               `json:"role"`
	Content []streamContentBlock `json:"content"`
}

type streamContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
}

// applyStreamMessage folds one stream-json line into state. It returns
// the final summary text and isResult=true for the terminating result
// line, and dirty=true whenever state gained something worth flushing.
// Unparseable lines (CLI warnings) are ignored.
func applyStreamMessage(state *streamState, line []byte) (string, bool, bool) {
	var msg streamMessage
	if err := json.Unmarshal(line, &msg); err != nil {
		return "", false, false
	}
	dirty := false
	switch msg.Type {
	case "system":
		if msg.SessionID != "" && state.SessionID == "" {
			state.SessionID = msg.SessionID
			dirty = true
		}
	case "assistant":
		if msg.Message == nil {
			break
		}
		for _, b := range msg.Message.Content {
			switch b.Type {
			case "text":
				if b.Text != "" {
					state.Events = append(state.Events, streamEvent{Type: "text", Text: b.Text})
					dirty = true
				}
			case "tool_use":
				state.Events = append(state.Events, streamEvent{Type: "tool_use", Tool: b.Name, Input: b.Input, ID: b.ID})
				dirty = true
			}
		}
	case "user":
		if msg.Message == nil {
			break
		}
		for _, b := range msg.Message.Content {
			if b.Type != "tool_result" {
				continue
			}
			state.Events = append(state.Events, streamEvent{
				Type: "tool_result", ToolUseID: b.ToolUseID,
				Content: flattenToolResultContent(b.Content), IsError: b.IsError,
			})
			dirty = true
		}
	case "result":
		if msg.SessionID != "" && state.SessionID == "" {
			state.SessionID = msg.SessionID
		}
		return msg.Result, true, true
	}
	return "", false, dirty
}

// flattenToolResultContent normalizes the wire shape of a
// tool_result.content field. Claude emits it either as a bare string
// or as an array of {type:"text", text:"..."} blocks; we present a
// single string to the frontend.
func flattenToolResultContent(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	// Try string form first.
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	// Fall back to content array form.
	var blocks []streamContentBlock
	if err := json.Unmarshal(raw, &blocks); err == nil {
		var b strings.Builder
		for i, blk := range blocks {
			if blk.Type != "text" {
				continue
			}
			if i > 0 {
				b.WriteString("\n")
			}
			b.WriteString(blk.Text)
		}
		return b.String()
	}
	// Unknown shape — keep raw bytes so reviewers can still see
	// something.
	return string(raw)
}
