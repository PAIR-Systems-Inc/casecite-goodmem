package app

import (
	"context"
	"fmt"
	"mime"
	"regexp"
	"strings"
	"time"
	"unicode"
)

func validateConversationExport(in M) (M, error) {
	out := M{"title": "Conversation", "format": "docx"}
	if value, present := in["title"]; present {
		text, ok := value.(string)
		if !ok || len([]rune(text)) > 300 {
			return nil, fail(422, "title must contain at most 300 characters")
		}
		out["title"] = text
	}
	if value, present := in["format"]; present {
		format, ok := value.(string)
		if !ok || format != "docx" && format != "pdf" && format != "md" {
			return nil, fail(422, "format must be docx, pdf or md")
		}
		out["format"] = format
	}
	messages, err := conversationObjects(in["messages"], 1, 500)
	if err != nil {
		return nil, fail(422, "messages must contain 1–500 message objects")
	}
	clean := []M{}
	for _, message := range messages {
		if message["role"] != "user" && message["role"] != "assistant" {
			return nil, fail(422, "Message role must be user or assistant")
		}
		row := M{"role": message["role"], "text": "", "citations": []M{}}
		if value, present := message["text"]; present {
			text, ok := value.(string)
			if !ok || len([]rune(text)) > 200000 {
				return nil, fail(422, "Message text must contain at most 200,000 characters")
			}
			row["text"] = text
		}
		if value, present := message["citations"]; present {
			citations, err := conversationObjects(value, 0, 200)
			if err != nil {
				return nil, fail(422, "citations must be a list of at most 200 citation objects")
			}
			cleanCitations := []M{}
			for _, citation := range citations {
				c := M{"label": nil, "quote": nil, "url": nil}
				for field, limit := range map[string]int{"label": 500, "quote": 5000, "url": 1000} {
					value := citation[field]
					if value == nil && field != "label" {
						continue
					}
					text, ok := value.(string)
					if !ok || len([]rune(text)) > limit || field == "label" && len(text) == 0 {
						return nil, fail(422, "Invalid citation "+field)
					}
					c[field] = text
				}
				cleanCitations = append(cleanCitations, c)
			}
			row["citations"] = cleanCitations
		}
		clean = append(clean, row)
	}
	out["messages"] = clean
	return out, nil
}

func conversationObjects(value any, low, high int) ([]M, error) {
	count := -1
	switch rows := value.(type) {
	case []any:
		count = len(rows)
	case []M:
		count = len(rows)
	}
	rows := maps(value)
	if count < low || count > high || count != len(rows) {
		return nil, fail(422, "Invalid object list")
	}
	return rows, nil
}

var conversationBold = regexp.MustCompile(`\*\*(.+?)\*\*`)
var conversationStrong = regexp.MustCompile(`__(.+?)__`)
var conversationCode = regexp.MustCompile("`([^`]+)`")
var conversationLink = regexp.MustCompile(`\[([^\]]+)\]\([^)]+\)`)

func conversationInline(text string) string {
	text = conversationBold.ReplaceAllString(text, "$1")
	text = conversationStrong.ReplaceAllString(text, "$1")
	for _, marker := range []rune{'*', '_'} {
		runes := []rune(text)
		word := func(r rune) bool { return unicode.IsLetter(r) || unicode.IsNumber(r) || r == '_' }
		var out strings.Builder
		for i := 0; i < len(runes); i++ {
			if runes[i] == marker && (i == 0 || !word(runes[i-1])) && i+1 < len(runes) && !unicode.IsSpace(runes[i+1]) {
				end := -1
				for j := i + 2; j < len(runes); j++ {
					if runes[j] == marker && !unicode.IsSpace(runes[j-1]) && (j+1 == len(runes) || !word(runes[j+1])) {
						end = j
						break
					}
					if runes[j] == '\n' {
						break
					}
				}
				if end >= 0 {
					out.WriteString(string(runes[i+1 : end]))
					i = end
					continue
				}
			}
			out.WriteRune(runes[i])
		}
		text = out.String()
	}
	text = conversationCode.ReplaceAllString(text, "$1")
	return conversationLink.ReplaceAllString(text, "$1")
}

func conversationBlocks(text string) []reportBlock {
	blocks := []reportBlock{}
	for _, raw := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line := strings.TrimRightFunc(raw, unicode.IsSpace)
		if strings.TrimSpace(line) == "" {
			continue
		}
		kind, content := "text", strings.TrimSpace(line)
		r := []rune(line)
		head := 0
		for head < len(r) && r[head] == '#' {
			head++
		}
		trimmed := []rune(strings.TrimLeftFunc(line, unicode.IsSpace))
		numbered := 0
		for numbered < len(trimmed) && unicode.IsDigit(trimmed[numbered]) {
			numbered++
		}
		switch {
		case head >= 1 && head <= 6 && head < len(r) && unicode.IsSpace(r[head]):
			kind, content = "subheading", strings.TrimSpace(string(r[head:]))
		case len(trimmed) > 1 && strings.ContainsRune("-*•", trimmed[0]) && unicode.IsSpace(trimmed[1]):
			kind, content = "unordered", strings.TrimSpace(string(trimmed[1:]))
		case numbered > 0 && numbered+1 < len(trimmed) && strings.ContainsRune(".)", trimmed[numbered]) && unicode.IsSpace(trimmed[numbered+1]):
			kind, content = "ordered", strings.TrimSpace(string(trimmed[numbered+1:]))
		}
		blocks = append(blocks, reportBlock{Kind: kind, Text: conversationInline(content)})
	}
	return blocks
}

func conversationFilename(title, format string) string {
	title = strings.Map(func(r rune) rune {
		if r < 128 && (r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == ' ' || r == '-' || r == '_') {
			return r
		}
		return -1
	}, title)
	stem := strings.Join(strings.Fields(title), "-")
	if len(stem) > 60 {
		stem = stem[:60]
	}
	return fallback(strings.Trim(stem, "-"), "conversation") + "." + format
}

func conversationMarkdown(in M, stamp string) string {
	lines := []string{"# " + fallback(str(in, "title"), "Conversation"), "", "_Exported " + stamp + "_", ""}
	for _, message := range maps(in["messages"]) {
		role := "Assistant"
		if message["role"] == "user" {
			role = "You"
		}
		lines = append(lines, "## "+role, "", strings.TrimSpace(str(message, "text")), "")
		citations := maps(message["citations"])
		if len(citations) > 0 {
			lines = append(lines, "**Sources**", "")
		}
		for i, c := range citations {
			line := fmt.Sprintf("%d. %s", i+1, str(c, "label"))
			if url := str(c, "url"); url != "" {
				line += " — " + url
			}
			lines = append(lines, line)
			if quote := str(c, "quote"); quote != "" {
				lines = append(lines, "   > "+strings.TrimSpace(quote))
			}
		}
		if len(citations) > 0 {
			lines = append(lines, "")
		}
	}
	lines = append(lines, "---", "", "_"+aiReviewNotice+"_")
	return strings.TrimRightFunc(strings.Join(lines, "\n"), unicode.IsSpace) + "\n"
}

func conversationReport(in M, stamp string) []reportBlock {
	blocks := []reportBlock{{Kind: "title", Text: fallback(str(in, "title"), "Conversation")}, {Kind: "stamp", Text: "Exported " + stamp}}
	for _, message := range maps(in["messages"]) {
		role := "Assistant"
		if message["role"] == "user" {
			role = "You"
		}
		blocks = append(blocks, reportBlock{Kind: "heading", Text: role})
		if message["role"] == "user" {
			for _, line := range strings.Split(strings.ReplaceAll(str(message, "text"), "\r\n", "\n"), "\n") {
				if strings.TrimSpace(line) != "" {
					blocks = append(blocks, reportBlock{Kind: "text", Text: conversationInline(strings.TrimSpace(line))})
				}
			}
		} else {
			blocks = append(blocks, conversationBlocks(str(message, "text"))...)
		}
		citations := maps(message["citations"])
		if len(citations) > 0 {
			blocks = append(blocks, reportBlock{Kind: "meta", Text: "Sources"})
		}
		for i, c := range citations {
			text := fmt.Sprintf("%d. %s", i+1, str(c, "label"))
			if url := str(c, "url"); url != "" {
				text += " — " + url
			}
			blocks = append(blocks, reportBlock{Kind: "text", Text: text})
			if quote := strings.TrimSpace(str(c, "quote")); quote != "" {
				blocks = append(blocks, reportBlock{Kind: "quote", Text: quote})
			}
		}
	}
	return append(blocks, reportBlock{Kind: "notice", Text: aiReviewNotice})
}

func renderConversation(ctx context.Context, in M, now time.Time) ([]byte, error) {
	stamp := now.UTC().Format("2006-01-02 15:04 UTC")
	if in["format"] == "md" {
		return []byte(conversationMarkdown(in, stamp)), nil
	}
	blocks := conversationReport(in, stamp)
	if in["format"] == "pdf" {
		return conversationPDF(ctx, blocks)
	}
	return reportWordDocument(blocks)
}

func (s *Server) exportConversation(q request) (any, error) {
	raw, err := body(q)
	if err != nil {
		return nil, err
	}
	in, err := validateConversationExport(raw)
	if err != nil {
		return nil, err
	}
	data, err := renderConversation(q.r.Context(), in, time.Now())
	if err != nil {
		return nil, err
	}
	types := map[string]string{"docx": docxMIME, "pdf": "application/pdf", "md": "text/markdown; charset=utf-8"}
	format := str(in, "format")
	q.w.Header().Set("Content-Type", types[format])
	q.w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": conversationFilename(str(in, "title"), format)}))
	_, err = q.w.Write(data)
	return nil, err
}
