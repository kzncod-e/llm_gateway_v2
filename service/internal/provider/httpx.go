package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const maxErrorBody = 64 << 10
const maxResponseBody = 16 << 20

// ErrorExtractor pulls a message and code out of a provider's error JSON body.
type ErrorExtractor func(body []byte) (message, code string)

// PostJSON sends a JSON body and returns the response if it has a 2xx status.
// Non-2xx responses are converted to *Error using extract.
func PostJSON(ctx context.Context, client *http.Client, url string, headers map[string]string, body any, extract ErrorExtractor) (*http.Response, error) {
	buf, err := json.Marshal(body)
	if err != nil {
		return nil, &Error{Kind: KindBadRequest, Message: "encode request: " + err.Error()}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return nil, &Error{Kind: KindBadRequest, Message: "build request: " + err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, FromTransport(ctx, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		defer resp.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		msg, code := extract(b)
		if msg == "" {
			msg = strings.TrimSpace(string(b))
			if len(msg) > 300 {
				msg = msg[:300]
			}
		}
		return nil, FromStatus(resp.StatusCode, msg, code, resp.Header)
	}
	return resp, nil
}

// DecodeJSON reads a bounded JSON response body.
func DecodeJSON(ctx context.Context, resp *http.Response, v any) error {
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	if err != nil {
		return FromTransport(ctx, err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		return &Error{Kind: KindInvalidResponse, Message: "decode provider response: " + err.Error(), Sent: true}
	}
	return nil
}

// SSEEvent is one server-sent event.
type SSEEvent struct {
	Event string
	Data  []byte
}

// SSEReader parses a text/event-stream body incrementally.
type SSEReader struct {
	r *bufio.Reader
}

func NewSSEReader(r io.Reader) *SSEReader {
	return &SSEReader{r: bufio.NewReaderSize(r, 64<<10)}
}

const maxSSELine = 8 << 20

// Next returns the next event, or io.EOF when the body ends. A trailing event
// without a terminating blank line is still returned; adapters decide whether a
// stream completed from provider-specific end markers.
func (s *SSEReader) Next() (*SSEEvent, error) {
	var ev SSEEvent
	var data [][]byte
	hasField := false
	for {
		line, err := s.readLine()
		if err != nil {
			if errors.Is(err, io.EOF) {
				if hasField {
					if len(data) > 0 {
						ev.Data = bytes.Join(data, []byte("\n"))
						return &ev, nil
					}
				}
				return nil, io.EOF
			}
			return nil, err
		}
		if len(line) == 0 {
			if len(data) > 0 || ev.Event != "" {
				ev.Data = bytes.Join(data, []byte("\n"))
				return &ev, nil
			}
			continue
		}
		if line[0] == ':' {
			continue // comment / keep-alive
		}
		hasField = true
		field, value, _ := bytes.Cut(line, []byte(":"))
		value = bytes.TrimPrefix(value, []byte(" "))
		switch string(field) {
		case "event":
			ev.Event = string(value)
		case "data":
			data = append(data, append([]byte(nil), value...))
		}
	}
}

func (s *SSEReader) readLine() ([]byte, error) {
	var line []byte
	for {
		chunk, isPrefix, err := s.r.ReadLine()
		if err != nil {
			return nil, err
		}
		line = append(line, chunk...)
		if len(line) > maxSSELine {
			return nil, fmt.Errorf("sse line exceeds %d bytes", maxSSELine)
		}
		if !isPrefix {
			return line, nil
		}
	}
}

// StreamReadError converts a read error from a streaming body into *Error.
func StreamReadError(ctx context.Context, err error) *Error {
	if ctx.Err() != nil {
		return FromTransport(ctx, err)
	}
	return &Error{Kind: KindStreamInterrupted, Message: "provider stream interrupted: " + err.Error(), Sent: true}
}

// MergeSameRole joins consecutive messages that share a role, which Anthropic and
// Gemini require to alternate.
func MergeSameRole(msgs []Message) []Message {
	out := make([]Message, 0, len(msgs))
	for _, m := range msgs {
		if n := len(out); n > 0 && out[n-1].Role == m.Role {
			out[n-1].Content += "\n\n" + m.Content
			continue
		}
		out = append(out, m)
	}
	return out
}

// SplitSystem separates system messages (joined) from the conversation.
func SplitSystem(msgs []Message) (system string, rest []Message) {
	var sys []string
	for _, m := range msgs {
		if m.Role == "system" {
			sys = append(sys, m.Content)
			continue
		}
		rest = append(rest, m)
	}
	return strings.Join(sys, "\n\n"), rest
}
