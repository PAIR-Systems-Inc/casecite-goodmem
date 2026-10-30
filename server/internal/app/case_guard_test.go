package app

import (
	"strings"
	"testing"
)

func TestCaseGuardRespectsSentenceBoundariesAndAbbreviations(t *testing.T) {
	for _, tc := range []struct {
		text    string
		names   []string
		want    string
		removed int
	}{
		{"Jane Example attended Example University. Notice v. Example appears in the profile.", []string{"Notice v. Example"}, "Jane Example attended Example University. Notice v. Example appears in the profile.", 0},
		{"Notice v. Example. Phantom v. Stranger.", []string{"Notice v. Example"}, "Notice v. Example. " + caseRedaction + ".", 1},
		{"Previous Fact. Phantom v. Stranger. Next Fact.", nil, "Previous Fact. " + caseRedaction + ". Next Fact.", 1},
		{"U.S. Department of Justice v. Example Corp. applies.", []string{"U.S. Department of Justice v. Example Corp."}, "U.S. Department of Justice v. Example Corp. applies.", 0},
		{"Acme Inc. Holdings v. A. Smith is listed.", []string{"Acme Inc. Holdings v. A. Smith"}, "Acme Inc. Holdings v. A. Smith is listed.", 0},
		{"Phantom Notice v. Example is different.", []string{"Notice v. Example"}, caseRedaction + " is different.", 1},
	} {
		t.Run(tc.text, func(t *testing.T) {
			refs := []M{}
			for _, name := range tc.names {
				refs = append(refs, M{"metadata": M{"case_name": name}})
			}
			got, removed := redactCaseReferences(tc.text, nil, refs, false)
			if got != tc.want || len(removed) != tc.removed {
				t.Fatalf("got %q, removed %v; want %q", got, removed, tc.want)
			}
			if strings.Contains(got, "Phantom") {
				t.Fatal("unverified reference survived", got)
			}
		})
	}
}
