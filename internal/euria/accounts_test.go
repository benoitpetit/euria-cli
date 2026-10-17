package euria

import (
	"context"
	"io"
	"net/http"
	"testing"
)

func TestAccountsDecodesNumericAndStringIDs(t *testing.T) {
	client, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/1/users/me/accounts" {
			t.Errorf("request = %s %s, want GET /api/1/users/me/accounts", r.Method, r.URL.Path)
		}
		_, _ = io.WriteString(w, `[{"id":42,"name":"Euria"},{"id":"84","name":"Work"}]`)
	}))
	got, err := client.Accounts(context.Background(), testSession())
	if err != nil {
		t.Fatalf("Accounts() error = %v", err)
	}
	if len(got) != 2 || got[0].ID != "42" || got[1].ID != "84" {
		t.Fatalf("Accounts() = %#v, want account IDs 42 and 84", got)
	}
}

func TestSelectAccountRejectsAmbiguousList(t *testing.T) {
	_, err := SelectAccount([]Account{{ID: "10"}, {ID: "20"}}, "")
	if err == nil {
		t.Fatal("SelectAccount() selected one of multiple accounts without a requested ID")
	}
}

func TestSelectAccountUsesRequestedID(t *testing.T) {
	got, err := SelectAccount([]Account{{ID: "10"}, {ID: "20"}}, "20")
	if err != nil {
		t.Fatalf("SelectAccount() error = %v", err)
	}
	if got.ID != "20" {
		t.Fatalf("SelectAccount().ID = %q, want %q", got.ID, "20")
	}
}
