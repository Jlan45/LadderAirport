package dnsproviders

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ladderairport/panel/internal/dnsprovider"
)

func TestCallbackProviderCRUD(t *testing.T) {
	actions := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var values map[string]string
		if err := json.NewDecoder(r.Body).Decode(&values); err != nil {
			t.Errorf("decode body: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if r.Header.Get("Authorization") != "Bearer callback-secret" {
			t.Errorf("authorization was not expanded")
		}
		actions = append(actions, values["action"])
		w.Header().Set("Content-Type", "application/json")
		switch values["action"] {
		case "lookup":
			_ = json.NewEncoder(w).Encode(callbackResponse{Records: []dnsprovider.Record{
				{Name: values["name"], Type: dnsprovider.RecordType(values["type"]), Value: "192.0.2.1"},
			}})
		default:
			_ = json.NewEncoder(w).Encode(callbackResponse{ID: "record-id"})
		}
	}))
	defer server.Close()

	providerRaw, err := newCallback(dnsprovider.Config{
		Credentials: map[string]string{"token": "callback-secret"},
		Settings: map[string]any{
			"url":                   server.URL,
			"method":                "POST",
			"headers":               map[string]any{"Authorization": "Bearer #{credential.token}"},
			"allow_private_network": true,
			"test_zone":             "example.com",
		},
		HTTPTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	provider := providerRaw.(*callbackProvider)
	zone := dnsprovider.Zone{Name: "example.com"}
	records, err := provider.Lookup(context.Background(), zone, "edge", dnsprovider.TypeA)
	if err != nil || len(records) != 1 {
		t.Fatalf("lookup = %+v, %v", records, err)
	}
	ref, err := provider.Upsert(context.Background(), zone, dnsprovider.Record{
		Name: "edge", Type: dnsprovider.TypeA, Value: "192.0.2.2", TTL: time.Minute,
	})
	if err != nil || ref.ID != "record-id" {
		t.Fatalf("upsert = %+v, %v", ref, err)
	}
	if err := provider.Delete(context.Background(), zone, ref); err != nil {
		t.Fatal(err)
	}
	if len(actions) != 3 || actions[0] != "lookup" || actions[1] != "upsert" || actions[2] != "delete" {
		t.Fatalf("actions = %v", actions)
	}
}

func TestCallbackRejectsUnsafeTargets(t *testing.T) {
	_, err := newCallback(dnsprovider.Config{Settings: map[string]any{
		"url": "http://127.0.0.1/callback",
	}})
	if err == nil {
		t.Fatal("accepted non-HTTPS private callback")
	}
}
