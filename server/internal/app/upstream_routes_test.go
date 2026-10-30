package app

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"regexp"
	"testing"
)

// This checks the pinned source inventory against the actual ServeMux, including
// dynamically registered route families. It is a completeness guard for routes,
// not a substitute for their authorization, response and behavior tests.
func TestEveryUpstreamRouteIsRegistered(t *testing.T) {
	data, err := os.ReadFile("../../../docs/parity/upstream-routes.json")
	if err != nil {
		t.Fatal(err)
	}
	var inventory struct {
		Commit string                                  `json:"upstream_commit"`
		Routes []struct{ Method, Path, Source string } `json:"routes"`
	}
	if err := json.Unmarshal(data, &inventory); err != nil {
		t.Fatal(err)
	}
	if inventory.Commit != "6605cb3474cf0c2894080af216d5fed2a81fc997" || len(inventory.Routes) < 196 {
		t.Fatal("missing or changed upstream route baseline")
	}
	f := newFixture(t)
	parameter := regexp.MustCompile(`\{[^}]+\}`)
	for _, route := range inventory.Routes {
		t.Run(route.Method+" "+route.Path, func(t *testing.T) {
			path := parameter.ReplaceAllString(route.Path, "1")
			_, pattern := f.s.mux.Handler(httptest.NewRequest(route.Method, path, nil))
			if pattern == "" || pattern == "/api/" || pattern == "/" && path != "/" {
				t.Fatalf("no application route for %s %s (original %s)", route.Method, route.Path, route.Source)
			}
		})
	}
}
