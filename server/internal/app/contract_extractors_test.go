package app

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
	"time"
)

func TestContractExtractorsMatchPythonOracle(t *testing.T) {
	b, err := os.ReadFile("testdata/contract_extractors.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Baseline string
		Cases    []struct {
			Name, Text string
			Effective  string `json:"effective_date"`
			Expected   M
		}
	}
	if err = json.Unmarshal(b, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.Baseline != "6605cb3474cf0c2894080af216d5fed2a81fc997" {
		t.Fatal("Unexpected oracle baseline")
	}
	for _, c := range fixture.Cases {
		t.Run(c.Name, func(t *testing.T) {
			effective, err := time.Parse("2006-01-02T15:04:05", c.Effective)
			if err != nil {
				t.Fatal(err)
			}
			parties := resolveParties(c.Text)
			obligations := extractObligations(c.Text, parties)
			deadlines := extractDeadlines(c.Text, &effective)
			linkDeadlines(obligations, deadlines)
			actual := M{"parties": parties, "obligations": obligations, "deadlines": deadlines, "defined_terms": checkDefinedTerms(c.Text)}
			var normalized M
			if err = json.Unmarshal(jsonBytes(actual), &normalized); err != nil {
				t.Fatal(err)
			}
			for key, want := range c.Expected {
				if !reflect.DeepEqual(normalized[key], want) {
					a, _ := json.MarshalIndent(normalized[key], "", "  ")
					b, _ := json.MarshalIndent(want, "", "  ")
					t.Errorf("%s differs from original Python:\ngot: %s\nwant: %s", key, a, b)
				}
			}
		})
	}
}
