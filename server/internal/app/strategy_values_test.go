package app

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"testing"
)

func TestStrategyPythonBehavior(t *testing.T) {
	data, err := os.ReadFile("testdata/strategy_behavior.json")
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		Baseline string `json:"baseline"`
		Fixtures []M    `json:"fixtures"`
	}
	if err = json.Unmarshal(data, &oracle); err != nil {
		t.Fatal(err)
	}
	if oracle.Baseline != "6605cb3474cf0c2894080af216d5fed2a81fc997" || len(oracle.Fixtures) != 39 {
		t.Fatal("wrong strategy baseline")
	}
	for i, f := range oracle.Fixtures {
		t.Run(fmt.Sprintf("%02d_%s", i, str(f, "kind")), func(t *testing.T) {
			var actual any
			switch str(f, "kind") {
			case "confidence":
				actual = strategyConfidence(judgeNumber(f, "score"))
			case "group":
				actual = strategyGroupChunks(maps(f["rows"]), stringsOf(f["allowed"]), number(f, "count", 3))
			case "passages":
				docs := []M{}
				for _, value := range f["order"].([]any) {
					pair := value.([]any)
					docs = append(docs, M{"id": pair[0], "filename": pair[1]})
				}
				grouped := map[string][]M{}
				for id, rows := range object(f["grouped"]) {
					grouped[id] = maps(rows)
				}
				actual = strategyPassages(docs, grouped)
			case "join":
				sections, citations := strategyJoinCitations(object(f["data"]), maps(f["passages"]))
				actual = M{"sections": sections, "citations": citations}
			default:
				t.Fatal("unknown fixture")
			}
			var normalized any
			_ = json.Unmarshal(jsonBytes(actual), &normalized)
			if !reflect.DeepEqual(normalized, f["expected"]) {
				t.Fatalf("got %s\nwant %s", jsonBytes(actual), jsonBytes(f["expected"]))
			}
		})
	}
}
