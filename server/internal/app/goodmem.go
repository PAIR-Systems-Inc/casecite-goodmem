package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"sort"
	"strings"
	"time"
)

type GoodMem struct {
	URL, Key, Space, Reranker string
	Client                    *http.Client
	OCRTimeout                time.Duration
	Chunking                  M
}
type GoodMemError struct{ Status int }

func (e *GoodMemError) Error() string { return fmt.Sprintf("GoodMem returned HTTP %d", e.Status) }

type Hit struct {
	ID, MemoryID, Text string
	Score              float64
	Sequence           int
	Metadata           M
	Stage              string
}

func NewGoodMem(c Config) *GoodMem {
	return &GoodMem{URL: c.GoodMemURL, Key: c.GoodMemKey, Space: c.GoodMemSpace, Reranker: c.RerankerID, OCRTimeout: 10 * time.Minute, Client: &http.Client{Timeout: 120 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

func (g *GoodMem) do(ctx context.Context, method, p string, body io.Reader, contentType string) (*http.Response, error) {
	r, err := http.NewRequestWithContext(ctx, method, g.URL+p, body)
	if err != nil {
		return nil, err
	}
	r.Header.Set("x-api-key", g.Key)
	r.Header.Set("Accept", "application/x-ndjson")
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	resp, err := g.Client.Do(r)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		resp.Body.Close()
		return nil, &GoodMemError{resp.StatusCode}
	}
	return resp, nil
}
func (g *GoodMem) JSON(ctx context.Context, method, p string, in any) (M, error) {
	var body io.Reader
	if in != nil {
		body = bytes.NewReader(jsonBytes(in))
	}
	r, err := g.do(ctx, method, p, body, "application/json")
	if err != nil {
		return nil, err
	}
	defer r.Body.Close()
	if r.StatusCode == 204 {
		return M{}, nil
	}
	m := M{}
	err = json.NewDecoder(io.LimitReader(r.Body, 16<<20)).Decode(&m)
	return m, err
}
func (g *GoodMem) Check(ctx context.Context) error {
	_, err := g.JSON(ctx, "GET", "/v1/spaces/"+url.PathEscape(g.Space), nil)
	return err
}
func (g *GoodMem) Create(ctx context.Context, id, docID, filename, contentType string, data []byte) (M, error) {
	return g.createMemory(ctx, id, docID, filename, contentType, data, "")
}
func (g *GoodMem) CreateTextIndex(ctx context.Context, id, docID, original, filename string, data []byte, method string) (M, error) {
	return g.createMemory(ctx, id, docID, filename, "text/plain", data, original, M{"text_extraction": method})
}
func (g *GoodMem) CreateReindex(ctx context.Context, id, docID, original, filename, contentType string, data []byte, ocr bool) (M, error) {
	extraction := "native"
	if ocr {
		extraction = "goodmem_ocr"
	}
	return g.createMemory(ctx, id, docID, filename, contentType, data, original, M{"text_extraction": extraction})
}
func (g *GoodMem) createMemory(ctx context.Context, id, docID, filename, contentType string, data []byte, original string, metadata ...M) (M, error) {
	var buffer bytes.Buffer
	w := multipart.NewWriter(&buffer)
	request := M{"memoryId": id, "spaceId": g.Space, "contentType": contentType, "extractPageImages": true, "metadata": M{"title": filename, "filename": filename, "casecite_document_id": docID, "casecite_memory_id": id}, "chunkingConfig": M{"recursive": M{"chunkSize": 1800, "chunkOverlap": 200, "lengthMeasurement": "CHARACTER_COUNT", "keepStrategy": "KEEP_END"}}}
	if g.Chunking != nil {
		request["chunkingConfig"] = g.Chunking
	}
	if original != "" {
		object(request["metadata"])["casecite_source_memory_id"] = original
		object(request["metadata"])["text_extraction"] = "goodmem_ocr"
		request["extractPageImages"] = false
	}
	for _, extra := range metadata {
		for k, v := range extra {
			object(request["metadata"])[k] = v
		}
	}
	if err := w.WriteField("request", string(jsonBytes(request))); err != nil {
		return nil, err
	}
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", `form-data; name="file"; filename="document"`)
	h.Set("Content-Type", contentType)
	p, err := w.CreatePart(h)
	if err != nil {
		return nil, err
	}
	if _, err = p.Write(data); err != nil {
		return nil, err
	}
	if err = w.Close(); err != nil {
		return nil, err
	}
	r, err := g.do(ctx, "POST", "/v1/memories", &buffer, w.FormDataContentType())
	if err != nil {
		return nil, err
	}
	defer r.Body.Close()
	m := M{}
	err = json.NewDecoder(r.Body).Decode(&m)
	return m, err
}
func (g *GoodMem) Memory(ctx context.Context, id string) (M, error) {
	return g.JSON(ctx, "GET", "/v1/memories/"+url.PathEscape(id), nil)
}
func (g *GoodMem) Wait(ctx context.Context, id string) (M, error) {
	t := time.NewTicker(500 * time.Millisecond)
	defer t.Stop()
	for {
		m, err := g.Memory(ctx, id)
		if err != nil {
			return nil, err
		}
		switch str(m, "processingStatus") {
		case "COMPLETED":
			return m, nil
		case "FAILED":
			return nil, errors.New("GoodMem document processing failed; inspect the memory processing history")
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-t.C:
		}
	}
}
func (g *GoodMem) Delete(ctx context.Context, id string) error {
	r, err := g.do(ctx, "DELETE", "/v1/memories/"+url.PathEscape(id), nil, "")
	var ge *GoodMemError
	if errors.As(err, &ge) && ge.Status == 404 {
		return nil
	}
	if err != nil {
		return err
	}
	r.Body.Close()
	return nil
}
func (g *GoodMem) Content(ctx context.Context, id string) ([]byte, error) {
	r, err := g.do(ctx, "GET", "/v1/memories/"+url.PathEscape(id)+"/content", nil, "")
	if err != nil {
		return nil, err
	}
	defer r.Body.Close()
	b, err := io.ReadAll(io.LimitReader(r.Body, 251<<20))
	if len(b) > 250<<20 {
		return nil, errors.New("document exceeds download limit")
	}
	return b, err
}
func (g *GoodMem) Pages(ctx context.Context, id string) ([]M, error) {
	out := []M{}
	next := ""
	for {
		p := "/v1/memories/" + url.PathEscape(id) + "/pages?maxResults=500"
		if next != "" {
			p += "&nextToken=" + url.QueryEscape(next)
		}
		m, err := g.JSON(ctx, "GET", p, nil)
		if err != nil {
			return nil, err
		}
		if a, ok := m["pageImages"].([]any); ok {
			for _, v := range a {
				out = append(out, object(v))
			}
		}
		next = str(m, "nextToken")
		if next == "" {
			return out, nil
		}
		if len(out) > 10000 {
			return nil, errors.New("too many page images")
		}
	}
}
func (g *GoodMem) Page(ctx context.Context, id, index string, query url.Values) (*http.Response, error) {
	p := "/v1/memories/" + url.PathEscape(id) + "/pages/" + url.PathEscape(index) + "/image"
	v := url.Values{}
	for _, k := range []string{"dpi", "contentType"} {
		if query.Get(k) != "" {
			v.Set(k, query.Get(k))
		}
	}
	if len(v) > 0 {
		p += "?" + v.Encode()
	}
	return g.do(ctx, "GET", p, nil, "")
}

// Search only active memory revisions selected from the application's SQL ACLs.
// Empty scope returns locally: it must never become an unfiltered remote query.
func (g *GoodMem) Search(ctx context.Context, query string, memoryIDs []string, k int) ([]Hit, error) {
	return g.SearchWithRerank(ctx, query, memoryIDs, k, true)
}

// Reranking is a per-request choice. In particular, the chat UI's toggle must
// not silently inherit an instance-wide postprocessor when switched off.
func (g *GoodMem) SearchWithRerank(ctx context.Context, query string, memoryIDs []string, k int, rerank bool) ([]Hit, error) {
	return g.searchWithSpaces(ctx, query, memoryIDs, nil, k, rerank)
}

func (g *GoodMem) searchWithSpaces(ctx context.Context, query string, memoryIDs []string, spaces map[string]string, k int, rerank bool) ([]Hit, error) {
	if len(memoryIDs) == 0 {
		return []Hit{}, nil
	}
	if len(query) > 10000 {
		query = string([]rune(query)[:min(10000, len([]rune(query)))])
	}
	out := []Hit{}
	for start := 0; start < len(memoryIDs); start += 100 {
		ids := memoryIDs[start:min(start+100, len(memoryIDs))]
		grouped := map[string][]string{}
		allowed := map[string]bool{}
		for _, id := range ids {
			space := fallback(spaces[id], g.Space)
			grouped[space] = append(grouped[space], "'"+strings.ReplaceAll(id, "'", "''")+"'")
			allowed[id] = true
		}
		keys := []M{}
		for space, quoted := range grouped {
			keys = append(keys, M{"spaceId": space, "filter": "CAST(val('$.casecite_memory_id') AS TEXT) IN (" + strings.Join(quoted, ",") + ")"})
		}
		sort.Slice(keys, func(i, j int) bool { return str(keys[i], "spaceId") < str(keys[j], "spaceId") })
		body := M{"message": query, "spaceKeys": keys, "requestedSize": k, "fetchMemory": true}
		if rerank && g.Reranker != "" {
			body["requestedSize"] = max(k, 30)
			body["postProcessor"] = M{"name": "com.goodmem.retrieval.postprocess.ChatPostProcessorFactory", "config": M{"reranker_id": g.Reranker, "max_results": k}}
		}
		r, err := g.do(ctx, "POST", "/v1/memories:retrieve", bytes.NewReader(jsonBytes(body)), "application/json")
		if err != nil {
			return nil, err
		}
		hits, err := decodeRetrieval(r.Body, allowed)
		r.Body.Close()
		if err != nil {
			return nil, err
		}
		out = append(out, hits...)
	}
	// GoodMem queries each space with its own embedder, then merges results.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Stage == "rerank" && out[j].Stage == "rerank" {
			return out[i].Score > out[j].Score
		}
		if (out[i].Stage == "rerank") != (out[j].Stage == "rerank") {
			return out[i].Stage == "rerank"
		}
		return out[i].Score < out[j].Score
	})
	if len(out) > k {
		out = out[:k]
	}
	return out, nil
}

func decodeRetrieval(r io.Reader, allowed map[string]bool) ([]Hit, error) {
	scan := bufio.NewScanner(r)
	scan.Buffer(make([]byte, 64<<10), 8<<20)
	memories := []M{}
	sets := map[string][]Hit{}
	stages := map[string]string{}
	order := []string{}
	stage := "vector_search"
	current := "default"
	for scan.Scan() {
		line := bytes.TrimSpace(scan.Bytes())
		if len(line) == 0 || bytes.HasPrefix(line, []byte("event:")) || bytes.HasPrefix(line, []byte(":")) {
			continue
		}
		line = bytes.TrimPrefix(line, []byte("data: "))
		m := M{}
		if err := json.Unmarshal(line, &m); err != nil {
			return nil, fmt.Errorf("invalid GoodMem retrieval event: %w", err)
		}
		if v, ok := m["memoryDefinition"]; ok {
			memories = append(memories, object(v))
			continue
		}
		if v, ok := m["resultSetBoundary"]; ok {
			b := object(v)
			if str(b, "kind") == "BEGIN" {
				current = str(b, "resultSetId")
				stage = str(b, "stageName")
				stages[current] = stage
				order = append(order, current)
				sets[current] = []Hit{}
			}
			continue
		}
		if v, ok := m["status"]; ok {
			status := object(v)
			code := str(status, "code")
			if code != "" && code != "OK" {
				return nil, errors.New("GoodMem retrieval reported " + code)
			}
		}
		ref := object(object(m["retrievedItem"])["chunk"])
		chunk := object(ref["chunk"])
		if len(chunk) == 0 {
			continue
		}
		mid := str(chunk, "memoryId")
		if mid == "" {
			i := number(ref, "memoryIndex", -1)
			if i >= 0 && i < len(memories) {
				mid = str(memories[i], "memoryId")
			}
		}
		if !allowed[mid] {
			continue
		}
		score, _ := ref["relevanceScore"].(float64)
		id := str(ref, "resultSetId")
		if id == "" {
			id = current
		}
		if _, ok := sets[id]; !ok {
			order = append(order, id)
		}
		setStage := fallback(stages[id], stage)
		sets[id] = append(sets[id], Hit{ID: str(chunk, "chunkId"), MemoryID: mid, Text: str(chunk, "chunkText"), Score: score, Sequence: number(chunk, "chunkSequenceNumber", 0), Metadata: object(chunk["metadata"]), Stage: setStage})
	}
	if err := scan.Err(); err != nil {
		return nil, err
	}
	if len(order) == 0 {
		return []Hit{}, nil
	}
	return sets[order[len(order)-1]], nil
}
