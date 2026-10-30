package app

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"
)

func chatStats(documents, chunks int, intent string) M {
	return M{"docs_searched": documents, "chunks_retrieved": chunks, "processing_time": "", "case_law_searched": 0, "case_law_included": 0, "query_intent": intent}
}

func (s *Server) perFileChatAnswer(ctx context.Context, user, query string, docs []M) (M, error) {
	if len(docs) < 2 || len(docs) > 8 {
		return nil, bad("Per-file chat requires 2–8 selected documents")
	}
	type file struct {
		Doc    M
		Text   string
		Answer M
		Err    error
	}
	files := make([]file, len(docs))
	missing := []string{}
	for i, doc := range docs {
		text, loaded, err := s.documentText(ctx, user, str(doc, "id"))
		if err != nil && chatReadFatal(err) {
			return nil, err
		}
		files[i] = file{Doc: doc, Text: text, Err: err}
		if loaded != nil {
			files[i].Doc = loaded
		}
		if err != nil || strings.TrimSpace(text) == "" {
			missing = append(missing, str(doc, "filename"))
			files[i].Text = ""
		}
	}
	var work sync.WaitGroup
	slots := make(chan struct{}, 4)
	for i := range files {
		if files[i].Text == "" {
			continue
		}
		work.Add(1)
		go func(i int) {
			defer work.Done()
			select {
			case slots <- struct{}{}:
			case <-ctx.Done():
				files[i].Err = ctx.Err()
				return
			}
			defer func() { <-slots }()
			f := &files[i]
			instruction := chatAssets.Instructions["build_map_prompt"] + "3 quotes)."
			note := ""
			if utf8.RuneCountInString(f.Text) > 60000 {
				note = "\nThe document is truncated after 60,000 characters. State where your answer may depend on the omitted remainder."
			}
			f.Answer, f.Err = s.structured(withLLMOptions(ctx, llmOptions{}), user, instruction,
				"Question: "+query+"\nUntrusted document JSON:\n"+string(jsonBytes(M{"filename": f.Doc["filename"], "document_id": f.Doc["id"], "text": charSlice(f.Text, 0, 60000)}))+note)
		}(i)
	}
	work.Wait()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	citations, findings := []M{}, []M{}
	sources := []string{}
	failed, truncated := []string{}, []string{}
	for _, f := range files {
		if f.Text == "" {
			continue
		}
		sources = append(sources, f.Text)
		if f.Err != nil || strings.TrimSpace(str(f.Answer, "answer")) == "" {
			failed = append(failed, str(f.Doc, "filename"))
			continue
		}
		findings = append(findings, M{"document_id": f.Doc["id"], "filename": f.Doc["filename"], "answer": f.Answer["answer"]})
		coverage := "Read in full"
		if utf8.RuneCountInString(f.Text) > 60000 {
			coverage = "Read the first 60,000 characters"
			truncated = append(truncated, str(f.Doc, "filename"))
		}
		quotes := maps(f.Answer["key_quotes"])
		kept := 0
		for _, quote := range quotes {
			text := strings.TrimSpace(str(quote, "quote"))
			if text == "" {
				continue
			}
			start, end, verified := findQuote(f.Text, text)
			if verified {
				start, end, verified = chatTidySpan(f.Text, start, end)
			}
			if verified {
				text = f.Text[start:end]
			}
			if chatContentRatio(text) < .45 {
				continue
			}
			c := chatCitation(str(f.Doc, "filename"), "document", text, len(citations)+1)
			c["id"], c["document_id"], c["was_cited_by_ai"], c["verified"] = "pf-"+str(f.Doc, "id")+fmt.Sprintf("-%d", len(citations)+1), f.Doc["id"], true, verified
			why := strings.TrimSpace(str(quote, "why"))
			c["application"] = nullable(why)
			matching := coverage + " — quote not found verbatim; treat this point as unverified."
			if verified {
				matching = coverage + fmt.Sprintf(" — verified verbatim at line %d.", strings.Count(f.Text[:start], "\n")+1)
				attachChatLocation(c, f.Text, f.Doc, start, end)
			}
			c["logic"] = M{"query_intent": query, "matching_criteria": matching, "application": why}
			c["coverage"] = coverage
			citations = append(citations, c)
			kept++
			if kept == 3 {
				break
			}
		}
	}
	if len(findings) == 0 {
		return nil, fail(502, "Per-file analysis could not read and analyze any of the selected documents.")
	}
	answer, stats, err := s.llm(withLLMOptions(ctx, llmOptions{}), user, legalSystem+"\n"+chatAssets.Instructions["build_reduce_prompt"],
		"Question: "+query+"\nUntrusted per-file findings JSON:\n"+string(jsonBytes(findings)), false)
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		current, err := s.getObject(ctx, user, "document", str(f.Doc, "id"))
		if err != nil {
			return nil, err
		}
		if str(current, "memory_id") != str(f.Doc, "memory_id") {
			return nil, fail(409, "A selected document changed during analysis. Retry the question.")
		}
	}
	answer, removed := redactCaseReferences(answer, sources, nil, false)
	if len(missing) > 0 {
		answer += "\n\nCould not read: " + strings.Join(missing, ", ") + ". These files were not analyzed."
	}
	if len(failed) > 0 {
		answer += "\n\nCould not analyze: " + strings.Join(failed, ", ") + ". The answer covers the other files only."
	}
	if len(truncated) > 0 {
		answer += "\n\nOnly the first 60,000 characters were analyzed for: " + strings.Join(truncated, ", ") + "."
	}
	if len(removed) > 0 {
		answer += "\n\nCase references absent from the selected documents were removed."
	}
	responseStats := chatStats(len(findings), len(citations), "per_file")
	responseStats["model"], responseStats["provider"] = stats["model"], stats["provider"]
	return M{"content": answer, "citations": citations, "stats": responseStats}, nil
}
