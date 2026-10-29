package app

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"testing"
)

func assertPythonResult(t *testing.T, got, want any) {
	t.Helper()
	var normalized any
	if err := json.Unmarshal(jsonBytes(got), &normalized); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(normalized, want) {
		a, _ := json.MarshalIndent(normalized, "", "  ")
		b, _ := json.MarshalIndent(want, "", "  ")
		t.Fatalf("Go result differs from pinned Python oracle:\ngot %s\nwant %s", a, b)
	}
}
func TestContractBehaviorMatchesPythonOracle(t *testing.T) {
	b, err := os.ReadFile("testdata/contract_behavior.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture M
	if err = json.Unmarshal(b, &fixture); err != nil {
		t.Fatal(err)
	}
	if str(fixture, "baseline") != "6605cb3474cf0c2894080af216d5fed2a81fc997" {
		t.Fatal("wrong oracle baseline")
	}
	t.Run("collect_findings", func(t *testing.T) {
		c := object(fixture["collect_findings"])
		assertPythonResult(t, collectFindings(object(c["clause"]), object(c["general"])), c["expected"])
	})
	for _, c := range maps(fixture["issues"]) {
		t.Run(str(c, "name"), func(t *testing.T) {
			assertPythonResult(t, mergeIssues(maps(c["findings"]), object(c["payload"])), c["expected"])
		})
	}
	for _, c := range maps(fixture["plans"]) {
		t.Run(str(c, "name"), func(t *testing.T) {
			plan, err := normalizeDraftPlan(object(c["input"]), number(c, "target", 0))
			if err != nil {
				t.Fatal(err)
			}
			assertPythonResult(t, plan, c["expected"])
		})
	}
	for _, c := range maps(fixture["references"]) {
		t.Run(str(c, "name"), func(t *testing.T) {
			refs := []draftReference{}
			for _, v := range c["references"].([]any) {
				pair := stringsOf(v)
				refs = append(refs, draftReference{pair[0], pair[1]})
			}
			block, mode := buildReferenceBlock(refs, number(c, "budget", 0), str(c, "focus"))
			assertPythonResult(t, []string{block, mode}, c["expected"])
		})
	}
	for i, c := range maps(fixture["split_sections"]) {
		t.Run(fmt.Sprintf("split-%d", i), func(t *testing.T) { assertPythonResult(t, splitDraftSections(str(c, "text")), c["expected"]) })
	}
	for i, c := range maps(fixture["reconcile"]) {
		t.Run(fmt.Sprintf("reconcile-%d", i), func(t *testing.T) {
			problem := checkReconciledSections(str(c, "text"), maps(c["sections"]))
			assertPythonResult(t, nullable(problem), c["expected"])
		})
	}
	for i, c := range maps(fixture["clauses"]) {
		t.Run(fmt.Sprintf("clause-%d", i), func(t *testing.T) {
			text := str(c, "text")
			canonical := object(c["canonical"])
			subs := detectSubElements(text, canonical)
			assertPythonResult(t, subs, c["sub_elements"])
			tag := mergeMap(spanFields(text, 0, len(text)), M{"confidence": 0.95})
			assertPythonResult(t, clauseDeviations(canonical, text, subs, tag), c["deviations"])
		})
	}
	for i, c := range maps(fixture["segments"]) {
		t.Run(fmt.Sprintf("segments-%d", i), func(t *testing.T) {
			out := []M{}
			text := str(c, "text")
			for _, s := range clauseSegments(text) {
				out = append(out, M{"text": s.Text, "start": len([]rune(text[:s.Start])), "end": len([]rune(text[:s.End]))})
			}
			assertPythonResult(t, out, c["expected"])
		})
	}
	for i, c := range maps(fixture["guard"]) {
		t.Run(fmt.Sprintf("case-guard-%d", i), func(t *testing.T) {
			text := str(c, "text")
			out := []string{}
			for _, span := range unverifiedCases(text, stringsOf(c["sources"]), nil) {
				out = append(out, text[span[0]:span[1]])
			}
			assertPythonResult(t, out, c["expected"])
		})
	}
}
