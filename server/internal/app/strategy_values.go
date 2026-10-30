package app

import (
	"fmt"
	"math"
	"slices"
	"strings"
)

func strategyConfidence(score float64) int { return int(math.RoundToEven(max(0, min(1, score)) * 100)) }

func strategyGroupChunks(rows []M, ids []string, count int) map[string][]M {
	out := map[string][]M{}
	for _, row := range rows {
		id := str(object(row["metadata"]), "document_id")
		if !slices.Contains(ids, id) {
			continue
		}
		if _, ok := out[id]; !ok {
			out[id] = []M{}
		}
		if len(out[id]) < count {
			out[id] = append(out[id], row)
		}
	}
	return out
}

func strategyPassages(docs []M, grouped map[string][]M) []M {
	out := []M{}
	for _, doc := range docs {
		for _, row := range grouped[str(doc, "id")] {
			text := strings.TrimSpace(str(row, "text"))
			if text == "" {
				continue
			}
			out = append(out, M{"ref": fmt.Sprintf("d%d", len(out)), "document_id": doc["id"], "filename": doc["filename"], "snippet": charSlice(text, 0, 400), "text": charSlice(text, 0, 4000), "score": judgeNumber(row, "similarity")})
		}
	}
	return out
}

func strategyJoinCitations(data M, passages []M) (M, []M) {
	byRef, cited := map[string]M{}, map[string]M{}
	for _, p := range passages {
		byRef[str(p, "ref")] = p
	}
	resolve := func(raw any) []string {
		ids := []string{}
		for _, ref := range stringsOf(raw) {
			p := byRef[ref]
			if p == nil {
				continue
			}
			if cited[ref] == nil {
				cited[ref] = M{"id": ref, "source": p["filename"], "type": "document", "document_id": p["document_id"], "passage": p["snippet"], "confidence": strategyConfidence(judgeNumber(p, "score"))}
			}
			if !slices.Contains(ids, ref) {
				ids = append(ids, ref)
			}
		}
		return ids
	}
	sections := M{"position": strings.TrimSpace(str(data, "position"))}
	for _, section := range []string{"strengths", "weaknesses", "next_steps"} {
		key := "point"
		if section == "next_steps" {
			key = "step"
		}
		points := []M{}
		for _, item := range maps(data[section]) {
			text := strings.TrimSpace(str(item, key))
			if text != "" {
				points = append(points, M{key: text, "citation_ids": resolve(item["refs"])})
			}
		}
		sections[section] = points
	}
	citations := []M{}
	for _, p := range passages {
		if c := cited[str(p, "ref")]; c != nil {
			citations = append(citations, c)
		}
	}
	return sections, citations
}
