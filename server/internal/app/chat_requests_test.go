package app

import (
	"encoding/json"
	"os"
	"testing"
)

func TestChatAPIOracle(t *testing.T) {
	raw, err := os.ReadFile("testdata/chat_api.json")
	if err != nil {
		t.Fatal(err)
	}
	fixture := M{}
	if err = json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	for i, tc := range maps(fixture["requests"]) {
		var out M
		var err error
		if tc["kind"] == "export" {
			out, err = validateConversationExport(object(tc["input"]))
		} else {
			out, err = validateChatRequest(object(tc["input"]), tc["kind"] == "strategy")
		}
		if (err == nil) != (tc["valid"] == true) {
			t.Fatalf("request %d validity: %v", i, err)
		}
		if err == nil && string(jsonBytes(out)) != string(jsonBytes(tc["expected"])) {
			t.Fatalf("request %d normalization: got %s want %s", i, jsonBytes(out), jsonBytes(tc["expected"]))
		}
	}
	inputs := []M{}
	for _, tc := range maps(fixture["citations"]) {
		inputs = append(inputs, object(tc["input"]))
	}
	actual := strategyChatCitations(M{"citations": inputs})
	for i, tc := range maps(fixture["citations"]) {
		normalized := M{}
		for key := range object(tc["expected"]) {
			normalized[key] = actual[i][key]
		}
		if string(jsonBytes(normalized)) != string(jsonBytes(tc["expected"])) {
			t.Fatalf("citation %d: got %s want %s", i, jsonBytes(normalized), jsonBytes(tc["expected"]))
		}
	}
}
