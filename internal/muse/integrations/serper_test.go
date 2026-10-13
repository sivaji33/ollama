package integrations

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSerperSearch(t *testing.T) {
	t.Setenv("SERPER_API_KEY", "test-secret")

	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Errorf("unexpected method: %s", r.Method)
			}
			if r.Header.Get("X-API-KEY") != "test-secret" {
				t.Error("missing Serper authentication")
			}
			fmt.Fprint(w, `{"organic":[{"title":"Angular","link":"https://angular.dev","snippet":"Angular framework","position":1}]}`)
		},
	))
	defer server.Close()

	client := NewClient()
	client.serperBase = server.URL

	result, err := client.SerperSearch(
		context.Background(),
		"Angular",
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Organic) != 1 ||
		result.Organic[0].Title != "Angular" {
		t.Fatalf("unexpected result: %+v", result)
	}
}
