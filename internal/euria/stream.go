package euria

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// ChatResult contains the completed answer and any conversation ID in the stream.
type ChatResult struct {
	Answer         string `json:"answer"`
	ConversationID string `json:"conversation_id,omitempty"`
}

// ReadStream joins Euria's server-sent events into one completed answer.
func ReadStream(stream io.Reader) (ChatResult, error) {
	return ReadStreamWithCallback(stream, nil)
}

// ReadStreamWithCallback is like ReadStream but invokes onDelta for each content
// chunk as it arrives (when non-nil).
func ReadStreamWithCallback(stream io.Reader, onDelta func(string)) (ChatResult, error) {
	if stream == nil {
		return ChatResult{}, errors.New("Euria returned an empty stream")
	}
	reader := bufio.NewReader(stream)
	var result ChatResult
	var eventLines []string
	completed := false
	consume := func() error {
		if len(eventLines) == 0 {
			return nil
		}
		payload := strings.TrimSpace(strings.Join(eventLines, "\n"))
		eventLines = nil
		if payload == "" {
			return nil
		}
		if payload == "</stream>" || payload == "[DONE]" {
			completed = true
			return nil
		}
		var event map[string]json.RawMessage
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			return errors.New("malformed Euria stream event")
		}
		if message := streamErrorMessage(event); message != "" {
			return fmt.Errorf("Euria stream error: %s", message)
		}
		var object string
		if raw := event["object"]; len(raw) != 0 {
			if err := json.Unmarshal(raw, &object); err != nil {
				return errors.New("malformed Euria stream event")
			}
		}
		if object == "chat.completion.chunk" {
			if raw := event["id"]; len(raw) != 0 {
				if err := json.Unmarshal(raw, &result.ConversationID); err != nil {
					return errors.New("malformed Euria stream event")
				}
			}
		}
		if raw := event["choices"]; len(raw) != 0 && string(raw) != "null" {
			var choices []struct {
				Delta json.RawMessage `json:"delta"`
			}
			if err := json.Unmarshal(raw, &choices); err != nil {
				return errors.New("malformed Euria stream event")
			}
			if len(choices) > 0 && len(choices[0].Delta) > 0 && string(choices[0].Delta) != "null" {
				var delta struct {
					Content json.RawMessage `json:"content"`
				}
				if err := json.Unmarshal(choices[0].Delta, &delta); err != nil {
					return errors.New("malformed Euria stream event")
				}
				if len(delta.Content) > 0 && string(delta.Content) != "null" {
					var content string
					if err := json.Unmarshal(delta.Content, &content); err != nil {
						return errors.New("malformed Euria stream event")
					}
					result.Answer += content
					if onDelta != nil {
						onDelta(content)
					}
				}
			}
		}
		return nil
	}

	for {
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
			switch {
			case line == "":
				if err := consume(); err != nil {
					return ChatResult{}, err
				}
			case strings.HasPrefix(line, "data:"):
				eventLines = append(eventLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
			case line == "</stream>":
				if err := consume(); err != nil {
					return ChatResult{}, err
				}
				completed = true
			case strings.HasPrefix(line, ":"), strings.HasPrefix(line, "event:"), strings.HasPrefix(line, "id:"), strings.HasPrefix(line, "retry:"):
				// These are SSE control fields; only data fields carry model output.
			}
			if completed {
				break
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return ChatResult{}, fmt.Errorf("read Euria stream: %w", err)
		}
	}
	if err := consume(); err != nil {
		return ChatResult{}, err
	}
	if !completed {
		return ChatResult{}, errors.New("Euria stream ended before its completion marker")
	}
	if strings.TrimSpace(result.Answer) == "" {
		return ChatResult{}, errors.New("Euria stream ended without an answer")
	}
	return result, nil
}

func streamErrorMessage(event map[string]json.RawMessage) string {
	if raw := event["error"]; len(raw) > 0 && string(raw) != "null" {
		return "Euria stream error"
	}
	var eventType string
	_ = json.Unmarshal(event["type"], &eventType)
	if eventType == "error" {
		return "Euria stream error"
	}
	return ""
}
