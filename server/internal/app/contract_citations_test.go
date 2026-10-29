package app

import "testing"

func TestContractCitationMarkersFollowSurvivingQuotes(t *testing.T) {
	text := "📜 Customer shall pay in thirty days. Provider shall supply services."
	answer, citations := verifiedContractCitations(text, "Invented claim [1]. Payment [2]. Services [4]. Unknown [9]. Payment again [2].", []any{"A fabricated quote.", "Customer shall pay in thirty days.", nil, "Provider shall supply services."})
	want := "Invented claim [unverified citation]. Payment [1]. Services [2]. Unknown [unverified citation]. Payment again [1]."
	if answer != want {
		t.Fatalf("citation markers misdirected: %q", answer)
	}
	if len(citations) != 2 || number(citations[0], "span_start", -1) != 3 {
		t.Fatal(citations)
	}
	if quote := str(citations[1], "quote"); quote != "Provider shall supply services." {
		t.Fatal(quote)
	}
}
