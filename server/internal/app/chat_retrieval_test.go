package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func chatRetrievalFixture(t *testing.T, respond func(M) []Hit) *GoodMem {
	t.Helper()
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/memories:retrieve" || r.Header.Get("x-api-key") != "test" {
			t.Error("unexpected GoodMem request", r.URL.Path)
			w.WriteHeader(400)
			return
		}
		var in M
		if json.NewDecoder(r.Body).Decode(&in) != nil {
			w.WriteHeader(400)
			return
		}
		hits := respond(in)
		stage := "retrieve"
		if len(hits) > 0 {
			stage = hits[0].Stage
		}
		enc := json.NewEncoder(w)
		_ = enc.Encode(M{"resultSetBoundary": M{"resultSetId": "set", "kind": "BEGIN", "stageName": stage}})
		for _, hit := range hits {
			_ = enc.Encode(M{"retrievedItem": M{"chunk": M{"resultSetId": "set", "relevanceScore": hit.Score, "chunk": M{"chunkId": hit.ID, "memoryId": hit.MemoryID, "chunkText": hit.Text, "chunkSequenceNumber": hit.Sequence}}}})
		}
		_ = enc.Encode(M{"resultSetBoundary": M{"resultSetId": "set", "kind": "END", "stageName": stage}})
	}))
	t.Cleanup(host.Close)
	return NewGoodMem(Config{GoodMemURL: host.URL, GoodMemKey: "test", GoodMemSpace: "space", RerankerID: "reranker"})
}

func TestChatRetrievalThresholdCoverageOCRIdentityAndToggle(t *testing.T) {
	requests := 0
	gm := chatRetrievalFixture(t, func(in M) []Hit {
		requests++
		if in["postProcessor"] != nil {
			t.Error("reranking toggle ignored")
		}
		filter := str(maps(in["spaceKeys"])[0], "filter")
		if strings.Contains(filter, "original-scan") {
			t.Error("searched original instead of OCR memory")
		}
		if strings.Contains(filter, "search-1") {
			return []Hit{
				{ID: "a", MemoryID: "search-1", Text: "rent due monthly", Score: -.9, Stage: "retrieve"},
				{ID: "b", MemoryID: "search-1", Text: "rent overdue penalty", Score: -.8, Sequence: 1, Stage: "retrieve"},
				{ID: "private", MemoryID: "secret", Text: "must not escape", Score: -1, Stage: "retrieve"},
			}
		}
		return []Hit{{ID: "c", MemoryID: "search-2", Text: "rent due annually", Score: -.05, Stage: "retrieve"}}
	})
	s := &Server{gm: gm}
	docs := []M{{"id": "one", "memory_id": "original-scan", "index_memory_id": "search-1", "filename": "Scan.pdf", "source": "local"}, {"id": "two", "memory_id": "search-2", "filename": "Lease.txt", "source": "local"}}
	settings := M{"top_k": 2, "similarity_threshold": .25, "enable_reranking": false, "hybrid_search": true}
	rows, warnings, err := s.retrieveChatDocuments(context.Background(), "user", "rent", docs, true, settings)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || requests != 2 || len(warnings) != 1 {
		t.Fatal(rows, warnings, requests)
	}
	if str(object(rows[0]["metadata"]), "document_id") != "one" || judgeNumber(rows[0], "similarity") != .9 || str(object(rows[1]["metadata"]), "document_id") != "two" || object(rows[1]["metadata"])["below_threshold"] != true {
		t.Fatal(rows)
	}
	before := requests
	rows, _, err = s.retrieveChatDocuments(context.Background(), "user", "rent", nil, true, settings)
	if err != nil || len(rows) != 0 || requests != before {
		t.Fatal("empty scope queried GoodMem", rows, err)
	}
}

func TestChatRerankerKeepsVectorThresholdAndProvenance(t *testing.T) {
	requests := 0
	gm := chatRetrievalFixture(t, func(in M) []Hit {
		requests++
		if in["postProcessor"] != nil {
			return []Hit{
				{ID: "weak", MemoryID: "m", Text: "weak", Score: 99, Stage: "rerank", Sequence: 2},
				{ID: "strong", MemoryID: "m", Text: "strong", Score: 10, Stage: "rerank", Sequence: 1},
				{ID: "first", MemoryID: "m", Text: "first", Score: -1, Stage: "rerank"},
			}
		}
		return []Hit{
			{ID: "first", MemoryID: "m", Text: "first", Score: -.9, Stage: "retrieve"},
			{ID: "strong", MemoryID: "m", Text: "strong", Score: -.8, Stage: "retrieve", Sequence: 1},
			{ID: "weak", MemoryID: "m", Text: "weak", Score: -.1, Stage: "retrieve", Sequence: 2},
		}
	})
	s := &Server{gm: gm}
	rows, notes, err := s.retrieveChatDocuments(context.Background(), "user", "terms", []M{{"id": "doc", "memory_id": "m"}}, false, M{"top_k": 3, "similarity_threshold": .25, "enable_reranking": true})
	if err != nil || len(rows) != 2 || len(notes) != 0 || requests != 2 {
		t.Fatal(rows, notes, err, requests)
	}
	if str(rows[0], "id") != "strong" || judgeNumber(rows[0], "similarity") != .8 || judgeNumber(rows[0], "raw_score") != -.8 || judgeNumber(rows[0], "rerank_score") != 10 {
		t.Fatal(rows)
	}
}

func TestGoodMemFallbackSortUsesActualStage(t *testing.T) {
	gm := chatRetrievalFixture(t, func(in M) []Hit {
		return []Hit{{ID: "weak", MemoryID: "m", Score: -.1, Stage: "retrieve"}, {ID: "strong", MemoryID: "m", Score: -.9, Stage: "retrieve"}}
	})
	hits, err := gm.Search(context.Background(), "terms", []string{"m"}, 2)
	if err != nil || len(hits) != 2 || hits[0].ID != "strong" {
		t.Fatal(hits, err)
	}
}

func TestChatWeakFallbackStillAppliesExplicitFilters(t *testing.T) {
	gm := chatRetrievalFixture(t, func(in M) []Hit {
		return []Hit{
			{ID: "excluded", MemoryID: "m", Text: "rent deposit", Score: -.9, Stage: "retrieve"},
			{ID: "weak", MemoryID: "m", Text: "rent monthly", Score: -.1, Stage: "retrieve", Sequence: 1},
		}
	})
	s := &Server{gm: gm}
	rows, notes, err := s.retrieveChatDocuments(context.Background(), "user", "rent NOT deposit", []M{{"id": "doc", "memory_id": "m"}}, false, M{"top_k": 3, "similarity_threshold": .25})
	if err != nil || len(rows) != 1 || str(rows[0], "id") != "weak" || len(notes) != 1 {
		t.Fatal(rows, notes, err)
	}
}

func TestGoodMemInterleavedStagesRetainOwnProvenance(t *testing.T) {
	stream := `{"resultSetBoundary":{"resultSetId":"raw","kind":"BEGIN","stageName":"retrieve"}}
{"resultSetBoundary":{"resultSetId":"ranked","kind":"BEGIN","stageName":"rerank"}}
{"retrievedItem":{"chunk":{"resultSetId":"raw","relevanceScore":-0.9,"chunk":{"chunkId":"a","memoryId":"m"}}}}
{"retrievedItem":{"chunk":{"resultSetId":"ranked","relevanceScore":2,"chunk":{"chunkId":"b","memoryId":"m"}}}}
`
	hits, err := decodeRetrieval(strings.NewReader(stream), map[string]bool{"m": true})
	if err != nil || len(hits) != 1 || hits[0].Stage != "rerank" {
		t.Fatal(hits, err)
	}
}
