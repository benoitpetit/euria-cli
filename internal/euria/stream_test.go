package euria

import (
	"io"
	"strings"
	"testing"
)

type fragmentReader struct {
	value string
	size  int
}

func (r *fragmentReader) Read(p []byte) (int, error) {
	if r.value == "" {
		return 0, io.EOF
	}
	count := r.size
	if count > len(r.value) {
		count = len(r.value)
	}
	if count > len(p) {
		count = len(p)
	}
	copy(p, r.value[:count])
	r.value = r.value[count:]
	return count, nil
}

func TestReadStreamAcrossChunkBoundaries(t *testing.T) {
	stream := "data: {\"object\":\"chat.completion.chunk\",\"choices\":[{\"delta\":{\"content\":\"Hello \"}}]}\n\n" +
		"data: {\"object\":\"chat.completion.chunk\",\"choices\":[{\"delta\":{\"content\":\"world\"}}]}\n\n" +
		"data: </stream>"
	got, err := ReadStream(&fragmentReader{value: stream, size: 3})
	if err != nil {
		t.Fatalf("ReadStream() error = %v", err)
	}
	if got.Answer != "Hello world" {
		t.Fatalf("ReadStream().Answer = %q, want %q", got.Answer, "Hello world")
	}
}

func TestReadStreamReturnsConversationID(t *testing.T) {
	stream := "data: {\"id\":\"conv-123\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"delta\":{\"content\":\"answer\"}}]}\n\n" + "data: [DONE]\n\n"
	got, err := ReadStream(strings.NewReader(stream))
	if err != nil {
		t.Fatalf("ReadStream() error = %v", err)
	}
	if got.ConversationID != "conv-123" {
		t.Fatalf("ReadStream().ConversationID = %q, want %q", got.ConversationID, "conv-123")
	}
}

func TestReadStreamSurfacesServerError(t *testing.T) {
	stream := "data: {\"error\":{\"message\":\"private@example.com rotated-session-secret\"}}\n\n"
	_, err := ReadStream(strings.NewReader(stream))
	if err == nil || !strings.Contains(err.Error(), "Euria stream error") {
		t.Fatalf("ReadStream() error = %v, want a generic stream error", err)
	}
	if strings.Contains(err.Error(), "private@example.com") || strings.Contains(err.Error(), "rotated-session-secret") {
		t.Fatalf("ReadStream() error disclosed server data: %v", err)
	}
}

func TestReadStreamRejectsMalformedData(t *testing.T) {
	_, err := ReadStream(strings.NewReader("data: {not json}\n\n"))
	if err == nil {
		t.Fatal("ReadStream() accepted malformed event data")
	}
}

func TestReadStreamRejectsTruncatedAnswer(t *testing.T) {
	stream := "data: {\"object\":\"chat.completion.chunk\",\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n"
	if _, err := ReadStream(strings.NewReader(stream)); err == nil {
		t.Fatal("ReadStream() accepted an answer without a completion marker")
	}
}

func TestReadStreamStopsAtCompletionMarker(t *testing.T) {
	stream := "data: {\"object\":\"chat.completion.chunk\",\"choices\":[{\"delta\":{\"content\":\"complete\"}}]}\n\n" +
		"data: </stream>\n\n" +
		"data: {not json}\n\n"
	got, err := ReadStream(strings.NewReader(stream))
	if err != nil {
		t.Fatalf("ReadStream() error = %v", err)
	}
	if got.Answer != "complete" {
		t.Fatalf("ReadStream().Answer = %q, want %q", got.Answer, "complete")
	}
}
