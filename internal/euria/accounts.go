package euria

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/benoitpetit/euria-cli/internal/session"
)

// Account identifies an Euria account used in authenticated API routes.
type Account struct {
	ID string
}

// Message is one message already stored in an Euria conversation.
type Message struct {
	ID       string `json:"id"`
	ParentID string `json:"parent_id"`
	Role     string `json:"role"`
	Content  string `json:"content"`
}

// Accounts returns the accounts visible to the browser session.
func (c *Client) Accounts(ctx context.Context, userSession session.Session) ([]Account, error) {
	response, err := c.send(ctx, http.MethodGet, "/api/1/users/me/accounts", nil, &userSession)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if !isSuccess(response.StatusCode) {
		return nil, responseError(response, false)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read Euria accounts response: %w", err)
	}
	var rawAccounts []struct {
		ID json.RawMessage `json:"id"`
	}
	if err := json.Unmarshal(body, &rawAccounts); err != nil {
		var wrapped struct {
			Data []struct {
				ID json.RawMessage `json:"id"`
			} `json:"data"`
		}
		if err := json.Unmarshal(body, &wrapped); err != nil {
			return nil, fmt.Errorf("decode Euria accounts response: %w", err)
		}
		rawAccounts = wrapped.Data
	}
	accounts := make([]Account, 0, len(rawAccounts))
	for _, raw := range rawAccounts {
		id, err := accountID(raw.ID)
		if err != nil {
			return nil, err
		}
		accounts = append(accounts, Account{ID: id})
	}
	return accounts, nil
}

// SelectAccount returns an unambiguous account selected by ID or by uniqueness.
func SelectAccount(accounts []Account, requestedID string) (Account, error) {
	if requestedID != "" {
		for _, account := range accounts {
			if account.ID == requestedID {
				return account, nil
			}
		}
		return Account{}, errors.New("the requested Euria account was not found; check --account ID")
	}
	switch len(accounts) {
	case 0:
		return Account{}, errors.New("no Euria account is available for this browser session")
	case 1:
		return accounts[0], nil
	default:
		ids := make([]string, 0, len(accounts))
		for _, account := range accounts {
			ids = append(ids, account.ID)
		}
		return Account{}, fmt.Errorf("multiple Euria accounts are available (%s); select one with --account ID", strings.Join(ids, ", "))
	}
}

// ConversationMessages returns the current message chain for a saved conversation.
func (c *Client) ConversationMessages(ctx context.Context, userSession session.Session, accountID, conversationID string) ([]Message, error) {
	accountID, err := pathSegment(accountID, "account")
	if err != nil {
		return nil, err
	}
	conversationID, err = pathSegment(conversationID, "conversation")
	if err != nil {
		return nil, err
	}
	path := "/api/1/accounts/" + accountID + "/conversations/" + conversationID + "/messages"
	response, err := c.send(ctx, http.MethodGet, path, nil, &userSession)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if !isSuccess(response.StatusCode) {
		return nil, responseError(response, false)
	}
	var messages []Message
	if err := json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(&messages); err != nil {
		return nil, fmt.Errorf("decode Euria conversation messages: %w", err)
	}
	return messages, nil
}

func accountID(raw json.RawMessage) (string, error) {
	var value string
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return "", errors.New("Euria account response has no ID")
	}
	if raw[0] == '"' {
		if err := json.Unmarshal(raw, &value); err != nil {
			return "", errors.New("Euria account response has an invalid ID")
		}
	} else {
		value = string(raw)
	}
	value, err := pathSegment(value, "account")
	if err != nil {
		return "", errors.New("Euria account response has an invalid ID")
	}
	return value, nil
}
