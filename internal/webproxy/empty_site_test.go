package webproxy

import (
	"encoding/json"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestEmptySiteAPIRulesRemainArrays(t *testing.T) {
	cfg, svc := newTestService(t)
	router := chi.NewRouter()
	RegisterRoutes(router, cfg, svc)
	created := callWebAPI(t, router, "POST", "/api/sites", `{"name":"first site","listen":"127.0.0.1:21301","enabled":false,"rules":[]}`)
	if created.Code != 200 {
		t.Fatalf("create: %d %s", created.Code, created.Body.String())
	}
	var one struct {
		Data struct {
			Rules json.RawMessage `json:"rules"`
		} `json:"data"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &one); err != nil {
		t.Fatal(err)
	}
	if string(one.Data.Rules) != "[]" {
		t.Fatalf("create rules = %s", one.Data.Rules)
	}
	// Existing configurations with nil rules must have the same response contract.
	cfg.Lock()
	cfg.Sites[0].Rules = nil
	cfg.Unlock()
	listed := callWebAPI(t, router, "GET", "/api/sites", "")
	var list struct {
		Data []struct {
			Rules json.RawMessage `json:"rules"`
		} `json:"data"`
	}
	if err := json.Unmarshal(listed.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Data) != 1 || string(list.Data[0].Rules) != "[]" {
		t.Fatalf("list = %s", listed.Body.String())
	}
}
