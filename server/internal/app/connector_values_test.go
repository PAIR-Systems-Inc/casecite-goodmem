package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

func TestConnectorPythonOracle(t *testing.T) {
	raw, err := os.ReadFile("testdata/connectors.json")
	if err != nil {
		t.Fatal(err)
	}
	fixture := M{}
	if err = json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture["baseline"] != "6605cb3474cf0c2894080af216d5fed2a81fc997" {
		t.Fatal("wrong source baseline")
	}
	for i, tc := range maps(fixture["requests"]) {
		t.Run(fmt.Sprintf("request_%d", i), func(t *testing.T) {
			input := object(tc["input"])
			expected := object(tc["expected"])
			var actual M
			var err error
			if tc["kind"] == "picker" {
				rows, e := validatePickerFiles(str(tc, "provider"), []any{input})
				err = e
				if e == nil {
					actual = rows[0]
				}
			} else {
				actual, err = validateConnectorSync(input, "", false)
				if tc["valid"] == true && str(expected, "folder_id") == "" && expected["sync_all"] == false {
					if err == nil {
						t.Fatal("missing scope accepted")
					}
					return
				}
				if tc["valid"] == true {
					expected["folder_id"] = nullable(str(expected, "folder_id"))
					expected["sync_all"] = str(expected, "folder_id") == ""
				}
			}
			if (err == nil) != (tc["valid"] == true) {
				t.Fatalf("validity: %v", err)
			}
			if err == nil && string(jsonBytes(actual)) != string(jsonBytes(expected)) {
				t.Fatalf("got %s want %s", jsonBytes(actual), jsonBytes(expected))
			}
		})
	}
	for i, tc := range maps(fixture["units"]) {
		t.Run(fmt.Sprintf("provider_%d_%s", i, str(tc, "provider")), func(t *testing.T) {
			provider := str(tc, "provider")
			var d connectorDefinition
			for _, item := range connectorDefinitions {
				if item.ID == provider {
					d = item
				}
			}
			if tc["kind"] == "auth_url" {
				values := map[string]string{}
				for k, v := range object(tc["values"]) {
					if text, ok := v.(string); ok {
						values[k] = text
					}
				}
				values["microsoft_connector_redirect_uri"] = values["microsoft_redirect_uri"]
				u, err := url.Parse(connectorAuthURL(d, values, str(tc, "state")))
				if err != nil {
					t.Fatal(err)
				}
				actual := M{"base": u.Scheme + "://" + u.Host + u.Path, "query": u.Query()}
				if string(jsonBytes(actual)) != string(jsonBytes(tc["expected"])) {
					t.Fatalf("got %s want %s", jsonBytes(actual), jsonBytes(tc["expected"]))
				}
				return
			}
			payload := object(tc["payload"])
			key := map[string]string{"google_drive": "files", "onedrive": "value", "box": "entries", "dropbox": "entries", "clio": "data", "filevine": "items", "netdocuments": "list", "imanage": "data"}[provider]
			scope := str(tc, "folder")
			if provider == "netdocuments" && scope == "" {
				key = "cabinets"
			}
			if provider == "onedrive" {
				scope, _, err = connectorMSID(scope)
				if err != nil {
					t.Fatal(err)
				}
			}
			expected := maps(tc["expected"])
			for n, row := range maps(payload[key]) {
				f := connectorParseFile(provider, row, scope)
				var actual M
				_ = json.Unmarshal(jsonBytes(f), &actual)
				// Extra provenance fields are allowed; all original metadata is kept.
				meta := M{}
				for k := range object(expected[n]["metadata"]) {
					meta[k] = f.Metadata[k]
				}
				actual["metadata"] = meta
				if string(jsonBytes(actual)) != string(jsonBytes(expected[n])) {
					t.Fatalf("file %d got %s want %s", n, jsonBytes(actual), jsonBytes(expected[n]))
				}
			}
		})
	}
}

func TestConnectorTransportBoundaries(t *testing.T) {
	for _, raw := range []string{"127.0.0.1", "10.0.0.1", "169.254.169.254", "100.64.0.1", "192.0.2.1", "198.18.0.1", "::1", "::ffff:127.0.0.1", "fc00::1", "2002:7f00:1::", "64:ff9b::7f00:1"} {
		if connectorPublicIP(netip.MustParseAddr(raw)) {
			t.Error("accepted internal address", raw)
		}
	}
	for _, raw := range []string{"8.8.8.8", "2606:4700:4700::1111"} {
		if !connectorPublicIP(netip.MustParseAddr(raw)) {
			t.Error("rejected public address", raw)
		}
	}
	for _, host := range []string{"evilbox.com", "box.com.evil.test", "box.com@evil.test"} {
		if connectorAllowedHost(host, []string{"box.com"}) {
			t.Error(host)
		}
	}
	for _, next := range []string{"https://evil.test/v1.0/next", "https://user:secret@graph.microsoft.com/v1.0/next", "http://graph.microsoft.com/v1.0/next", "https://graph.microsoft.com/elsewhere"} {
		if _, err := connectorPageURL(next, "https://graph.microsoft.com/v1.0"); err == nil {
			t.Error(next)
		}
	}
	stamp := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	for raw, want := range map[string]time.Duration{"999": 30 * time.Second, "0": 0, "bad": 2 * time.Second, stamp.Add(9 * time.Second).Format(http.TimeFormat): 9 * time.Second} {
		if got := connectorRetryDelay(raw, 0, stamp); got != want {
			t.Errorf("delay %q got %v want %v", raw, got, want)
		}
	}
	encoded := connectorASCIIJSON(M{"path": "/café📜\".pdf"})
	if strings.ContainsAny(encoded, "é📜") {
		t.Fatal(encoded)
	}
	var decoded M
	if json.Unmarshal([]byte(encoded), &decoded) != nil || decoded["path"] != "/café📜\".pdf" {
		t.Fatal(encoded)
	}
	if _, _, err := connectorMSID("drv:broken"); err == nil {
		t.Fatal("malformed composite ID accepted")
	}
}
