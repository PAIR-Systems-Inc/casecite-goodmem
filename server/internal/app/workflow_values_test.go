package app

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestWorkflowUpstreamOracle(t *testing.T) {
	b, err := os.ReadFile("testdata/workflows.json")
	if err != nil {
		t.Fatal(err)
	}
	fixture := M{}
	if err = json.Unmarshal(b, &fixture); err != nil {
		t.Fatal(err)
	}
	for i, c := range maps(fixture["validation"]) {
		t.Run(fmt.Sprintf("validation_%d", i), func(t *testing.T) {
			steps, issues := validateWorkflowSteps(c["steps"], 12)
			if (len(issues) == 0) != c["valid"] {
				t.Fatalf("validity differs: got %s; upstream %s", jsonBytes(issues), jsonBytes(c["issues"]))
			}
			if c["valid"] == true && string(jsonBytes(steps)) != string(jsonBytes(c["normalized"])) {
				t.Fatalf("normalization differs:\nGo: %s\nPython: %s", jsonBytes(steps), jsonBytes(c["normalized"]))
			}
		})
	}
	for i, c := range maps(fixture["conditions"]) {
		t.Run(fmt.Sprintf("condition_%d", i), func(t *testing.T) {
			got := evaluateWorkflowCondition(object(c["condition"]), object(c["outputs"]))
			if string(jsonBytes(got)) != string(jsonBytes(c["expected"])) {
				t.Fatalf("Go: %s\nPython: %s", jsonBytes(got), jsonBytes(c["expected"]))
			}
		})
	}
	for i, c := range maps(fixture["substitution"]) {
		t.Run(fmt.Sprintf("substitution_%d", i), func(t *testing.T) {
			got := renderWorkflowTemplate(str(c, "text"), object(c["outputs"]))
			if string(jsonBytes(got)) != string(jsonBytes(c["expected"])) {
				t.Fatalf("substitution differs: %s", str(c, "text"))
			}
		})
	}
}

func TestWorkflowOutputCapIncludesMarkersAndPreservesProvenance(t *testing.T) {
	output := M{"summary": "Done", "document_ids": []string{"d1"}, "produced": M{"analyses": []string{"a1"}}, "data": M{"draft": strings.Repeat("📜 quotes \"\\\n", 5000), "rows": []M{{"blob": strings.Repeat("x", 6000)}, {"blob": "tail"}}}}
	for _, limit := range []int{4096, 8000, 12000} {
		got, err := capWorkflowOutput(output, limit)
		if err != nil {
			t.Fatal(err)
		}
		if len(jsonBytes(got)) > limit || got["truncated"] != true || len(stringsOf(got["truncated_fields"])) == 0 {
			t.Fatalf("invalid cap: %d bytes", len(jsonBytes(got)))
		}
		for _, key := range []string{"summary", "document_ids", "produced"} {
			if string(jsonBytes(got[key])) != string(jsonBytes(output[key])) {
				t.Fatalf("lost provenance: %s", key)
			}
		}
	}
	if len(str(object(output["data"]), "draft")) < 10000 {
		t.Fatal("cap mutated caller's data")
	}
	if _, err := capWorkflowOutput(M{"summary": strings.Repeat("x", 6000), "data": M{}}, 4096); err == nil {
		t.Fatal("unbounded metadata accepted")
	}
	_, issues := validateWorkflowSteps([]M{{"name": "a", "kind": "approval"}, {"name": "b", "kind": "approval", "condition": M{"step": "a", "op": "gt", "value": "NaN"}}}, 12)
	if len(issues) == 0 {
		t.Fatal("NaN condition accepted")
	}
}
