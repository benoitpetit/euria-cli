package euria

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"

	"github.com/benoitpetit/euria_retro/internal/session"
)

// TranscribeAudio sends an audio file to Euria for transcription (authenticated).
func (c *Client) TranscribeAudio(ctx context.Context, userSession session.Session, accountID, language, filePath string) (string, error) {
	accountID, err := pathSegment(accountID, "account")
	if err != nil {
		return "", err
	}
	return c.transcribe(ctx, "/api/1/accounts/"+accountID+"/audios/transcript", &userSession, language, filePath)
}

// TranscribeAudioGuest sends an audio file to Euria for transcription (guest).
func (c *Client) TranscribeAudioGuest(ctx context.Context, language, filePath string) (string, error) {
	return c.transcribe(ctx, "/api/1/public/audios/transcript", nil, language, filePath)
}

func (c *Client) transcribe(ctx context.Context, path string, userSession *session.Session, language, filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("open audio file: %w", err)
	}
	defer file.Close()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", filepath.Base(filePath))
	if err != nil {
		return "", fmt.Errorf("create form file: %w", err)
	}
	if _, err := io.Copy(part, file); err != nil {
		return "", fmt.Errorf("copy audio file: %w", err)
	}
	if err := writer.Close(); err != nil {
		return "", fmt.Errorf("close multipart writer: %w", err)
	}

	target := *c.origin
	target.Path = path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target.String(), &body)
	if err != nil {
		return "", fmt.Errorf("create transcription request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	if language != "" {
		req.Header.Set("Accept-Language", language)
	}
	req.Header.Set("Accept", "text/plain, application/json")

	client := c.requestClient(userSession)
	if userSession != nil {
		if token := xsrfCookie(client.Jar, c.origin, userSession.XsrfToken); token != "" {
			req.Header.Set("X-XSRF-TOKEN", token)
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("Euria request failed: %w", ctx.Err())
		}
		return "", errors.New("Euria request failed (network or TLS error)")
	}
	defer resp.Body.Close()
	if !isSuccess(resp.StatusCode) {
		return "", responseError(resp, userSession == nil)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", fmt.Errorf("read transcription response: %w", err)
	}
	text := string(data)
	// Euria returns either a JSON string or {"result":"success","data":"..."}.
	var parsedString string
	if err := json.Unmarshal(data, &parsedString); err == nil {
		text = parsedString
	} else {
		var parsedObject struct {
			Result string `json:"result"`
			Data   string `json:"data"`
			Text   string `json:"text"`
		}
		if err := json.Unmarshal(data, &parsedObject); err == nil {
			if parsedObject.Data != "" {
				text = parsedObject.Data
			} else if parsedObject.Text != "" {
				text = parsedObject.Text
			}
		}
	}
	return text, nil
}
