package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"testing"
)

func TestAdministrationPythonOracle(t *testing.T) {
	raw, err := os.ReadFile("testdata/administration.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Baseline string
		Reindex  []struct {
			Outcomes []string
			Expected M
		}
		Clear []struct {
			Cleared, Failed, Status int
			Expected                M
		}
	}
	if err = json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.Baseline != "6605cb3474cf0c2894080af216d5fed2a81fc997" {
		t.Fatal(fixture.Baseline)
	}
	for n, c := range fixture.Reindex {
		t.Run(fmt.Sprintf("reindex-%d", n), func(t *testing.T) {
			count := 0
			failures := []string{}
			for i, outcome := range c.Outcomes {
				if outcome == "indexed" {
					count++
					continue
				}
				reason := map[string]string{"failed": "indexing failed", "missing": "file not found", "gone": "document not found", "provider": "provider unavailable"}[outcome]
				failures = append(failures, fmt.Sprintf("file-%d.pdf: %s", i, reason))
			}
			var normalized M
			_ = json.Unmarshal(jsonBytes(reindexSummary(count, failures, "fixture-embedder")), &normalized)
			if !reflect.DeepEqual(normalized, c.Expected) {
				t.Fatalf("got %v want %v", normalized, c.Expected)
			}
		})
	}
	for n, c := range fixture.Clear {
		t.Run(fmt.Sprintf("clear-%d", n), func(t *testing.T) {
			got, err := clearSummary(c.Cleared, c.Failed)
			if c.Status != 200 {
				var ae *apiError
				if !errors.As(err, &ae) || ae.status != c.Status {
					t.Fatal(got, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			// Messages explicitly describe partial remote cleanup instead of claiming
			// "nothing was deleted" after one of several remote deletes succeeded.
			var normalized M
			_ = json.Unmarshal(jsonBytes(got), &normalized)
			for _, key := range []string{"status", "cleared_documents", "failed_documents"} {
				if !reflect.DeepEqual(normalized[key], c.Expected[key]) {
					t.Fatal(got, c.Expected)
				}
			}
		})
	}
}
