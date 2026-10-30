package app

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestPythonChatBehavior(t *testing.T) {
	data, err := os.ReadFile("testdata/chat_behavior.json")
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
	if oracle.Baseline != "6605cb3474cf0c2894080af216d5fed2a81fc997" || len(oracle.Fixtures) < 140 {
		t.Fatal("invalid Python baseline")
	}
	for i, fixture := range oracle.Fixtures {
		t.Run(fmt.Sprintf("%03d_%s", i, str(fixture, "kind")), func(t *testing.T) {
			var actual any
			switch str(fixture, "kind") {
			case "route", "payload":
				route := heuristicChatRoute(str(fixture, "query"))
				if fixture["kind"] == "payload" {
					route = parseChatRoute(str(fixture, "raw"), str(fixture, "query"))
				}
				value := M{"intent": route.Intent, "use_case_law": route.UseCaseLaw, "search_query": nullable(route.SearchQuery)}
				if fixture["kind"] == "route" {
					value["per_file"] = route.PerFile
				}
				actual = value
			case "intent":
				actual = chatQueryIntent(str(fixture, "query"), "")
			case "query":
				parsed := parseChatSearch(str(fixture, "query"))
				var value M
				if err := json.Unmarshal(jsonBytes(parsed), &value); err != nil {
					t.Fatal(err)
				}
				value["semantic_query"] = parsed.Semantic()
				actual = value
			case "bm25":
				rows := chatBM25(maps(fixture["documents"]), str(fixture, "query"))
				expected := fixture["expected"].([]any)
				if len(rows) != len(expected) {
					t.Fatalf("rows %v; expected %v", rows, expected)
				}
				for j, row := range rows {
					pair := expected[j].([]any)
					if row["id"] != pair[0] || math.Abs(judgeNumber(row, "score")-pair[1].(float64)) > 1e-10 {
						t.Fatalf("row %v; expected %v", row, pair)
					}
				}
				return
			case "compression":
				actual = compressChatResults(maps(fixture["documents"]), str(fixture, "query"), number(fixture, "count", 4))
			case "reference":
				citation := copyWorkflowMap(object(fixture["citation"]))
				verifyChatReferences([]M{citation}, str(fixture, "answer"))
				actual = citation
				// Intentional upstream defect fix: the stem "a" in a.pdf
				// cannot prove citation use merely by appearing in "payment".
				if str(citation, "source") == "a.pdf" {
					expected := copyWorkflowMap(object(fixture["expected"]))
					expected["was_cited_by_ai"] = false
					expected["notes"] = "Not referenced in answer"
					fixture["expected"] = expected
				}
			case "merge":
				lists := [][]M{}
				for _, list := range fixture["lists"].([]any) {
					lists = append(lists, maps(list))
				}
				actual = mergeChatResults(lists, number(fixture, "count", 0))
			case "tidy":
				value := str(fixture, "text")
				start, end, ok := chatTidySpan(value, 0, len(value))
				if ok {
					actual = []int{utf8.RuneCountInString(value[:start]), utf8.RuneCountInString(value[:end])}
				}
				if math.Abs(chatContentRatio(value)-judgeNumber(fixture, "ratio")) > 1e-10 {
					t.Fatal("content ratio", value)
				}
			case "excerpt":
				ranges := [][2]int{}
				for _, raw := range fixture["ranges"].([]any) {
					pair := raw.([]any)
					ranges = append(ranges, [2]int{int(pair[0].(float64)), int(pair[1].(float64))})
				}
				actual = chatFileExcerpt(strings.Repeat(str(fixture, "repeated_text"), number(fixture, "repeats", 1)), ranges)
			case "reasoning":
				actual = normalizeChatReasoning(fixture["raw"])
			default:
				t.Fatal("unknown fixture", fixture["kind"])
			}
			var normalized any
			if err := json.Unmarshal(jsonBytes(actual), &normalized); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(normalized, fixture["expected"]) {
				t.Fatalf("input %s\ngot %s\nwant %s", jsonBytes(fixture), jsonBytes(normalized), jsonBytes(fixture["expected"]))
			}
		})
	}
}

func TestChatClassifierFalseDoesNotEnableCaseLaw(t *testing.T) {
	route := parseChatRoute(`{"intent":"ask","use_case_law":"false","per_file":"false"}`, "find case law for our case")
	if route.UseCaseLaw || route.PerFile || route.Intent != "ask" {
		t.Fatal(route)
	}
	if got := parseChatRoute("```json\n{\"intent\":\"research\",\"use_case_law\":true}\n```", "Read the documents"); got.Intent != "research" || !got.UseCaseLaw {
		t.Fatal("lost fenced classifier response", got)
	}
}

func TestChatCitationSourceMustActuallyBeNamed(t *testing.T) {
	for _, tc := range []struct {
		source, answer string
		want           bool
	}{{"a.pdf", "The payment is due.", false}, {"a.pdf", "See a.pdf for the amount.", true}, {"report.pdf", "The reporting obligation applies.", false}, {"report.pdf", "The report explains the amount.", true}, {"report.pdf", "Éreport shows an unrelated filename.", false}} {
		c := M{"source": tc.source, "passage": ""}
		verifyChatReferences([]M{c}, tc.answer)
		if c["was_cited_by_ai"] != tc.want {
			t.Fatal(tc, c)
		}
	}
}
func TestChatMergePreservesDifferentBoilerplatePassages(t *testing.T) {
	prefix := strings.Repeat("shared contract boilerplate ", 10)
	rows := mergeChatResults([][]M{{{"text": prefix + "rent $10"}, {"text": prefix + "rent $100"}}}, 10)
	if len(rows) != 2 {
		t.Fatal("different clauses collapsed", rows)
	}
}
func TestChatAdvertisedFieldFilters(t *testing.T) {
	row := M{"text": "The lease renews annually.", "metadata": M{"filename": "Acme Lease.pdf", "source": "local", "judge": "Jones", "date_filed": "2024-01-15", "jurisdiction": "Federal"}}
	for _, query := range []string{`filename:"Acme Lease" source:local`, `judge:jones year:2024`, `after:2024 before:2025 jurisdiction:federal`} {
		if !chatResultMatches(row, parseChatSearch(query)) {
			t.Fatal("lost matching row", query)
		}
	}
	for _, query := range []string{`filename:other`, `source:box`, `judge:smith`, `year:2023`, `jurisdiction:state`, `lease -renews`} {
		if chatResultMatches(row, parseChatSearch(query)) {
			t.Fatal("ignored filter", query)
		}
	}
}
func TestChatCompressionDoesNotAlterSourceAndExpansionIsBounded(t *testing.T) {
	rows := []M{{"text": "Rent is due. Birds fly. Cats sleep. Trees grow. Rent increases."}}
	before := string(jsonBytes(rows))
	compressed := compressChatResults(rows, "rent", 2)
	if string(jsonBytes(rows)) != before || !strings.Contains(str(compressed[0], "text"), "Rent increases") {
		t.Fatal(compressed)
	}
	queries := chatExpandedQueries("Lease termination", "1. lease termination\n- Contract cancellation\n• Ending the lease\nNo extra request")
	if !reflect.DeepEqual(queries, []string{"Lease termination", "Contract cancellation", "Ending the lease"}) {
		t.Fatal(queries)
	}
}
