package euria

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/benoitpetit/euria_retro/internal/session"
)

// Conversation is one saved Euria conversation.
type Conversation struct {
	ID         string    `json:"id"`
	Title      string    `json:"title"`
	AccountID  int       `json:"account_id"`
	UserID     int       `json:"user_id"`
	ProjectID  string    `json:"project_id,omitempty"`
	IsFavorite bool      `json:"is_favorite"`
	IsArchived bool      `json:"is_archived"`
	IsShared   bool      `json:"is_shared"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	Model      string    `json:"model,omitempty"`
}

// SharedConversation is the result of sharing a conversation.
type SharedConversation struct {
	ID             string `json:"id"`
	ConversationID string `json:"conversation_id,omitempty"`
}

// Conversations lists the (first page of) conversations for an account.
func (c *Client) Conversations(ctx context.Context, userSession session.Session, accountID string) ([]Conversation, error) {
	accountID, err := pathSegment(accountID, "account")
	if err != nil {
		return nil, err
	}
	response, err := c.send(ctx, http.MethodGet, "/api/1/accounts/"+accountID+"/conversations", nil, &userSession)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if !isSuccess(response.StatusCode) {
		return nil, responseError(response, false)
	}
	return decodeConversations(response.Body)
}

func (c *Client) ArchivedConversations(ctx context.Context, userSession session.Session, accountID string) ([]Conversation, error) {
	accountID, err := pathSegment(accountID, "account")
	if err != nil {
		return nil, err
	}
	response, err := c.send(ctx, http.MethodGet, "/api/1/accounts/"+accountID+"/conversations/archived", nil, &userSession)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if !isSuccess(response.StatusCode) {
		return nil, responseError(response, false)
	}
	return decodeConversations(response.Body)
}

func (c *Client) FavoriteConversations(ctx context.Context, userSession session.Session, accountID string) ([]Conversation, error) {
	accountID, err := pathSegment(accountID, "account")
	if err != nil {
		return nil, err
	}
	response, err := c.send(ctx, http.MethodGet, "/api/1/accounts/"+accountID+"/conversations/favorites", nil, &userSession)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if !isSuccess(response.StatusCode) {
		return nil, responseError(response, false)
	}
	return decodeConversations(response.Body)
}

func (c *Client) GetConversation(ctx context.Context, userSession session.Session, accountID, conversationID string) (Conversation, error) {
	accountID, err := pathSegment(accountID, "account")
	if err != nil {
		return Conversation{}, err
	}
	conversationID, err = pathSegment(conversationID, "conversation")
	if err != nil {
		return Conversation{}, err
	}
	response, err := c.send(ctx, http.MethodGet, "/api/1/accounts/"+accountID+"/conversations/"+conversationID, nil, &userSession)
	if err != nil {
		return Conversation{}, err
	}
	defer response.Body.Close()
	if !isSuccess(response.StatusCode) {
		return Conversation{}, responseError(response, false)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return Conversation{}, fmt.Errorf("read Euria conversation: %w", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return Conversation{}, fmt.Errorf("decode Euria conversation: %w", err)
	}
	return mapConversation(raw), nil
}

func (c *Client) SearchConversations(ctx context.Context, userSession session.Session, accountID, query string) ([]Conversation, error) {
	accountID, err := pathSegment(accountID, "account")
	if err != nil {
		return nil, err
	}
	path := "/api/1/accounts/" + accountID + "/conversations/search"
	if query != "" {
		path += "?q=" + url.QueryEscape(query)
	}
	response, err := c.send(ctx, http.MethodGet, path, nil, &userSession)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if !isSuccess(response.StatusCode) {
		return nil, responseError(response, false)
	}
	return decodeConversations(response.Body)
}

func (c *Client) RenameConversation(ctx context.Context, userSession session.Session, accountID, conversationID, title string) error {
	accountID, err := pathSegment(accountID, "account")
	if err != nil {
		return err
	}
	conversationID, err = pathSegment(conversationID, "conversation")
	if err != nil {
		return err
	}
	payload := map[string]string{"title": title}
	response, err := c.send(ctx, http.MethodPut, "/api/1/accounts/"+accountID+"/conversations/"+conversationID, payload, &userSession)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if !isSuccess(response.StatusCode) {
		return responseError(response, false)
	}
	return nil
}

func (c *Client) ArchiveConversation(ctx context.Context, userSession session.Session, accountID, conversationID string) error {
	return c.postConversationAction(ctx, userSession, accountID, conversationID, "archive")
}

func (c *Client) UnarchiveConversation(ctx context.Context, userSession session.Session, accountID, conversationID string) error {
	return c.postConversationAction(ctx, userSession, accountID, conversationID, "unarchive")
}

func (c *Client) FavoriteConversation(ctx context.Context, userSession session.Session, accountID, conversationID string) error {
	return c.postConversationAction(ctx, userSession, accountID, conversationID, "favorite")
}

func (c *Client) UnfavoriteConversation(ctx context.Context, userSession session.Session, accountID, conversationID string) error {
	accountID, err := pathSegment(accountID, "account")
	if err != nil {
		return err
	}
	conversationID, err = pathSegment(conversationID, "conversation")
	if err != nil {
		return err
	}
	response, err := c.send(ctx, http.MethodDelete, "/api/1/accounts/"+accountID+"/conversations/"+conversationID+"/favorite", nil, &userSession)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if !isSuccess(response.StatusCode) {
		return responseError(response, false)
	}
	return nil
}

func (c *Client) ShareConversation(ctx context.Context, userSession session.Session, accountID, conversationID string) (SharedConversation, error) {
	accountID, err := pathSegment(accountID, "account")
	if err != nil {
		return SharedConversation{}, err
	}
	conversationID, err = pathSegment(conversationID, "conversation")
	if err != nil {
		return SharedConversation{}, err
	}
	response, err := c.send(ctx, http.MethodPost, "/api/1/accounts/"+accountID+"/conversations/"+conversationID+"/share", nil, &userSession)
	if err != nil {
		return SharedConversation{}, err
	}
	defer response.Body.Close()
	if !isSuccess(response.StatusCode) {
		return SharedConversation{}, responseError(response, false)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return SharedConversation{}, fmt.Errorf("read share response: %w", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		// maybe returns id directly
		return SharedConversation{ID: string(body)}, nil
	}
	share := SharedConversation{}
	if v, ok := raw["id"].(string); ok {
		share.ID = v
	}
	if v, ok := raw["conversation_id"].(string); ok {
		share.ConversationID = v
	}
	return share, nil
}

func (c *Client) postConversationAction(ctx context.Context, userSession session.Session, accountID, conversationID, action string) error {
	accountID, err := pathSegment(accountID, "account")
	if err != nil {
		return err
	}
	conversationID, err = pathSegment(conversationID, "conversation")
	if err != nil {
		return err
	}
	response, err := c.send(ctx, http.MethodPost, "/api/1/accounts/"+accountID+"/conversations/"+conversationID+"/"+action, nil, &userSession)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if !isSuccess(response.StatusCode) {
		return responseError(response, false)
	}
	return nil
}

func decodeConversations(body io.Reader) ([]Conversation, error) {
	data, err := io.ReadAll(io.LimitReader(body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("read conversations: %w", err)
	}
	// Try paginated shape first.
	var paginated struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(data, &paginated); err == nil && paginated.Data != nil {
		result := make([]Conversation, 0, len(paginated.Data))
		for _, raw := range paginated.Data {
			result = append(result, mapConversation(raw))
		}
		return result, nil
	}
	var list []map[string]any
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("decode conversations: %w", err)
	}
	result := make([]Conversation, 0, len(list))
	for _, raw := range list {
		result = append(result, mapConversation(raw))
	}
	return result, nil
}

func mapConversation(raw map[string]any) Conversation {
	conv := Conversation{}
	if v, ok := raw["id"].(string); ok {
		conv.ID = v
	}
	if v, ok := raw["title"].(string); ok {
		conv.Title = v
	}
	if v, ok := raw["account_id"].(float64); ok {
		conv.AccountID = int(v)
	}
	if v, ok := raw["user_id"].(float64); ok {
		conv.UserID = int(v)
	}
	if v, ok := raw["project_id"].(string); ok {
		conv.ProjectID = v
	}
	if v, ok := raw["is_favorite"].(bool); ok {
		conv.IsFavorite = v
	}
	if v, ok := raw["deleted_at"]; ok && v != nil {
		conv.IsArchived = true
	}
	if v, ok := raw["is_shared"].(bool); ok {
		conv.IsShared = v
	}
	if v, ok := raw["model"].(string); ok {
		conv.Model = v
	}
	if v, ok := raw["created_at"].(string); ok {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			conv.CreatedAt = t
		}
	} else if v, ok := raw["created_at"].(float64); ok {
		conv.CreatedAt = time.Unix(int64(v), 0)
	}
	if v, ok := raw["updated_at"].(string); ok {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			conv.UpdatedAt = t
		}
	} else if v, ok := raw["updated_at"].(float64); ok {
		conv.UpdatedAt = time.Unix(int64(v), 0)
	}
	return conv
}
