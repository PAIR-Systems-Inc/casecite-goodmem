package app

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
)

const wordNamespace = "http://schemas.openxmlformats.org/wordprocessingml/2006/main"
const strictWordNamespace = "http://purl.oclc.org/ooxml/wordprocessingml/main"

type wordNode struct {
	Name                            xml.Name
	Attrs                           []xml.Attr
	Start, OpenEnd, CloseStart, End int
	Text                            string
	TextStart, TextEnd              int
	Parent                          *wordNode
	Children                        []*wordNode
}

func readWordXML(data []byte) (*wordNode, error) {
	root := &wordNode{End: len(data), CloseStart: len(data)}
	stack := []*wordNode{root}
	d := xml.NewDecoder(bytes.NewReader(data))
	for {
		start := int(d.InputOffset())
		t, err := d.RawToken()
		end := int(d.InputOffset())
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch v := t.(type) {
		case xml.StartElement:
			p := stack[len(stack)-1]
			n := &wordNode{Name: v.Name, Attrs: v.Attr, Start: start, OpenEnd: end, Parent: p}
			p.Children = append(p.Children, n)
			stack = append(stack, n)
		case xml.EndElement:
			if len(stack) < 2 {
				return nil, bad("Malformed Word XML")
			}
			n := stack[len(stack)-1]
			if n.Name != v.Name {
				return nil, bad("Mismatched Word XML elements")
			}
			n.CloseStart, n.End = start, end
			stack = stack[:len(stack)-1]
		case xml.CharData:
			stack[len(stack)-1].Text += string(v)
		case xml.Directive:
			return nil, bad("Word XML directives are not supported")
		}
	}
	if len(stack) != 1 {
		return nil, bad("Incomplete Word XML")
	}
	return root, nil
}

func walkWord(n *wordNode, visit func(*wordNode)) {
	visit(n)
	for _, child := range n.Children {
		walkWord(child, visit)
	}
}
func wordTag(n *wordNode) string {
	if n.Name.Space == "" {
		return n.Name.Local
	}
	return n.Name.Space + ":" + n.Name.Local
}

// RawToken preserves the original prefixes and byte offsets. Resolve prefixes
// separately so DrawingML text or a different namespace's "body" is never
// mistaken for document text, and strict OOXML remains strict after editing.
func wordNodeNamespace(n *wordNode) string {
	prefix := n.Name.Space
	for p := n; p != nil; p = p.Parent {
		for _, a := range p.Attrs {
			if (prefix != "" && a.Name.Space == "xmlns" && a.Name.Local == prefix) ||
				(prefix == "" && a.Name.Space == "" && a.Name.Local == "xmlns") {
				return a.Value
			}
		}
	}
	return ""
}
func isWord(n *wordNode, local string) bool {
	if n.Name.Local != local {
		return false
	}
	ns := wordNodeNamespace(n)
	return ns == wordNamespace || ns == strictWordNamespace
}
func xmlText(v string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(v))
	return b.String()
}

// Text offsets are UTF-8 byte positions internally. HTTP/UI spans are UTF-16;
// convert at the boundary so emoji and non-ASCII parties cannot shift an edit.
func byteOffsetUTF16(text string, offset int) (int, bool) {
	if offset < 0 {
		return 0, false
	}
	units := 0
	for i, r := range text {
		if units == offset {
			return i, true
		}
		units += len(utf16.Encode([]rune{r}))
		if units > offset {
			return 0, false
		}
	}
	return len(text), units == offset
}

func wordVisibleText(root *wordNode, reject map[string]bool, maxParagraphs int) string {
	var out strings.Builder
	paragraphs := 0
	var walk func(*wordNode, bool)
	walk = func(n *wordNode, inDeleted bool) {
		if maxParagraphs > 0 && paragraphs >= maxParagraphs {
			return
		}
		id := ""
		for _, a := range n.Attrs {
			if a.Name.Local == "id" {
				id = a.Value
			}
		}
		if isWord(n, "del") || isWord(n, "moveFrom") {
			if !reject[id] {
				return
			}
			inDeleted = true
		}
		if isWord(n, "ins") && reject[id] {
			return
		}
		n.TextStart = out.Len()
		if isWord(n, "t") || (isWord(n, "delText") && inDeleted) {
			out.WriteString(n.Text)
		}
		if isWord(n, "tab") {
			out.WriteByte('\t')
		}
		if isWord(n, "br") || isWord(n, "cr") {
			out.WriteByte('\n')
		}
		for _, child := range n.Children {
			walk(child, inDeleted)
		}
		n.TextEnd = out.Len()
		if isWord(n, "p") {
			out.WriteByte('\n')
			paragraphs++
		}
	}
	walk(root, false)
	return out.String()
}

type anchoredEdit struct {
	Data       M
	Start, End int
	Last       *wordNode
}
type RedlineDocument struct {
	Content []byte
	Applied []M
	Skipped []M
}

// redlineDOCX patches only affected runs in word/document.xml. All other ZIP
// members, paragraph properties, styles, numbering, tables, headers, images,
// relationships and untouched XML bytes are copied from the source package.
func redlineDOCX(source []byte, edits []M, author string, includeNotice ...bool) (RedlineDocument, error) {
	result := RedlineDocument{Applied: []M{}, Skipped: []M{}}
	z, err := zip.NewReader(bytes.NewReader(source), int64(len(source)))
	if err != nil {
		return result, bad("A valid DOCX source is required")
	}
	var document []byte
	for _, f := range z.File {
		if f.Name == "word/document.xml" {
			if f.UncompressedSize64 > 32<<20 {
				return result, bad("Word document is too large")
			}
			r, e := f.Open()
			if e != nil {
				return result, e
			}
			document, e = io.ReadAll(io.LimitReader(r, (32<<20)+1))
			r.Close()
			if e != nil {
				return result, e
			}
		}
	}
	if len(document) == 0 {
		return result, bad("Word document body is missing")
	}
	root, err := readWordXML(document)
	if err != nil {
		return result, err
	}
	text := wordVisibleText(root, nil, 0)
	paragraphCount := 0
	revision := 0
	var body *wordNode
	tokens := []*wordNode{}
	walkWord(root, func(n *wordNode) {
		if isWord(n, "p") {
			paragraphCount++
		}
		if isWord(n, "body") {
			body = n
		}
		if isWord(n, "t") || isWord(n, "tab") || isWord(n, "br") || isWord(n, "cr") {
			if n.TextEnd > n.TextStart {
				tokens = append(tokens, n)
			}
		}
		for _, a := range n.Attrs {
			if a.Name.Local == "id" {
				if id, e := strconv.Atoi(a.Value); e == nil && id > revision {
					revision = id
				}
			}
		}
	})
	if body == nil {
		return result, bad("Word document body is missing")
	}
	namespace := wordNodeNamespace(body)
	prefixes := map[string]string{}
	walkWord(root, func(n *wordNode) {
		for _, a := range n.Attrs {
			if a.Name.Space == "xmlns" {
				if old, found := prefixes[a.Name.Local]; found && old != a.Value {
					prefixes[a.Name.Local] = "mixed"
				} else {
					prefixes[a.Name.Local] = a.Value
				}
			}
		}
	})
	prefix := "w"
	if bound, exists := prefixes[prefix]; exists && bound != namespace {
		prefix = "ccw"
		for prefixes[prefix] != "" {
			prefix += "x"
		}
	}
	// Only templates pass through this function, never user text or source XML.
	markup := func(template string) string {
		return strings.NewReplacer("w:", prefix+":", "xmlns:w=", "xmlns:"+prefix+"=").Replace(template)
	}
	anchored := []anchoredEdit{}
	insertions := []M{}
	skip := func(e M, reason string) {
		copy := M{}
		for k, v := range e {
			copy[k] = v
		}
		copy["export_reason"] = reason
		result.Skipped = append(result.Skipped, copy)
	}
	for _, e := range edits {
		if str(e, "kind") == "insert" {
			if str(e, "proposed_text") != "" {
				insertions = append(insertions, e)
			}
			continue
		}
		quote := str(e, "original_text")
		if quote == "" {
			skip(e, "No original quotation was supplied")
			continue
		}
		start, ok := byteOffsetUTF16(text, number(e, "span_start", -1))
		end := start + len(quote)
		if !ok || end > len(text) || text[start:end] != quote {
			// Prefer a matching stored position even when extraction only changed
			// whitespace. Otherwise require an unambiguous quoted passage.
			matchedStored := false
			if ok {
				if a, b, found := findQuote(text[start:], quote); found && a == 0 {
					end, matchedStored = start+b, true
				}
			}
			if !matchedStored {
				start, end, ok = findQuote(text, quote)
			}
			if !matchedStored && !ok {
				skip(e, "Original quotation was not found in the stored document")
				continue
			}
			_, _, repeated := findQuote(text[end:], quote)
			if !matchedStored && repeated {
				skip(e, "Original quotation occurs more than once and its stored span does not match")
				continue
			}
		}
		overlap := false
		for _, old := range anchored {
			if start < old.End && old.Start < end {
				overlap = true
			}
		}
		if overlap {
			skip(e, "Overlaps another selected edit")
			continue
		}
		var last *wordNode
		for _, n := range tokens {
			if n.TextStart < end && start < n.TextEnd {
				last = n
			}
		}
		if last == nil {
			skip(e, "The span has no editable text")
			continue
		}
		anchored = append(anchored, anchoredEdit{e, start, end, last})
		result.Applied = append(result.Applied, e)
	}
	slices.SortFunc(anchored, func(a, b anchoredEdit) int { return a.Start - b.Start })
	date := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	reject := map[string]bool{}
	wrap := func(kind, content string) string {
		if content == "" {
			return ""
		}
		// One id per revision element, including each fragment of a quote
		// split across formatted runs. Never reuse ids already in the file.
		revision++
		id := strconv.Itoa(revision)
		reject[id] = true
		return markup(`<w:`+kind+` xmlns:w="`) + namespace + markup(`" w:id="`) + id + markup(`" w:author="`) + xmlText(author) + markup(`" w:date="`) + date + `">` + content + markup(`</w:`+kind+`>`)
	}
	replacements := map[*wordNode]string{}
	walkWord(root, func(run *wordNode) {
		if !isWord(run, "r") {
			return
		}
		affected := false
		for _, n := range run.Children {
			for _, e := range anchored {
				if n.TextStart < e.End && e.Start < n.TextEnd && (isWord(n, "t") || isWord(n, "tab") || isWord(n, "br") || isWord(n, "cr")) {
					affected = true
				}
			}
		}
		if !affected {
			return
		}
		properties := ""
		for _, n := range run.Children {
			if isWord(n, "rPr") {
				properties = string(document[n.Start:n.End])
			}
		}
		opening := string(document[run.Start:run.OpenEnd])
		closing := "</" + wordTag(run) + ">"
		clone := func(content string) string {
			if content == "" {
				return ""
			}
			return opening + properties + content + closing
		}
		makeText := func(text, tag string) string {
			if text == "" {
				return ""
			}
			return markup(`<w:`+tag+` xmlns:w="`) + namespace + `" xml:space="preserve">` + xmlText(text) + markup(`</w:`+tag+`>`)
		}
		var out strings.Builder
		for _, n := range run.Children {
			if isWord(n, "rPr") {
				continue
			}
			if !isWord(n, "t") && !isWord(n, "tab") && !isWord(n, "br") && !isWord(n, "cr") {
				out.WriteString(clone(string(document[n.Start:n.End])))
				continue
			}
			pos := n.TextStart
			for _, e := range anchored {
				if e.Start >= n.TextEnd || e.End <= n.TextStart {
					continue
				}
				cut, end := max(pos, e.Start), min(n.TextEnd, e.End)
				if cut > pos {
					out.WriteString(clone(makeText(text[pos:cut], "t")))
				}
				deleted := clone(makeText(text[cut:end], "delText"))
				if !isWord(n, "t") {
					// Preserve real tab/line-break tokens and their attributes.
					deleted = clone(string(document[n.Start:n.End]))
				}
				out.WriteString(wrap("del", deleted))
				pos = end
				if e.Last == n {
					proposed := strings.ReplaceAll(str(e.Data, "proposed_text"), "\r\n", "\n")
					pieces := strings.Split(proposed, "\n")
					var content strings.Builder
					for i, piece := range pieces {
						if i > 0 {
							content.WriteString(markup(`<w:br xmlns:w="`) + namespace + `"/>`)
						}
						content.WriteString(makeText(piece, "t"))
					}
					out.WriteString(wrap("ins", clone(content.String())))
				}
			}
			if pos < n.TextEnd {
				if pos == n.TextStart {
					out.WriteString(clone(string(document[n.Start:n.End])))
				} else {
					out.WriteString(clone(makeText(text[pos:n.TextEnd], "t")))
				}
			}
		}
		replacements[run] = out.String()
	})
	var render func(*wordNode) string
	render = func(n *wordNode) string {
		if replacement, ok := replacements[n]; ok {
			return replacement
		}
		var b strings.Builder
		pos := n.Start
		for _, child := range n.Children {
			b.Write(document[pos:child.Start])
			b.WriteString(render(child))
			pos = child.End
		}
		b.Write(document[pos:n.End])
		return b.String()
	}
	patched := []byte(render(root))
	check, err := readWordXML(patched)
	if err != nil {
		return result, err
	}
	if recovered := wordVisibleText(check, reject, paragraphCount); recovered != text {
		return result, fmt.Errorf("redline fidelity check failed: rejecting proposed edits would change the original text")
	}
	// Missing clauses are explicit tracked insertions at the end, never a guessed location.
	var appendix strings.Builder
	paragraph := func(value string) string {
		return markup(`<w:p xmlns:w="`) + namespace + markup(`"><w:r><w:t xml:space="preserve">`) + xmlText(value) + markup(`</w:t></w:r></w:p>`)
	}
	for _, e := range insertions {
		appendix.WriteString(paragraph("Proposed addition — " + fallback(str(e, "title"), str(e, "ref"))))
		for _, line := range strings.Split(strings.ReplaceAll(str(e, "proposed_text"), "\r\n", "\n"), "\n") {
			appendix.WriteString(markup(`<w:p xmlns:w="`) + namespace + `">` + wrap("ins", markup(`<w:r><w:t xml:space="preserve">`)+xmlText(line)+markup(`</w:t></w:r>`)) + markup(`</w:p>`))
		}
		result.Applied = append(result.Applied, e)
	}
	if len(result.Skipped) > 0 {
		appendix.WriteString(paragraph("CaseCite export notes — unapplied proposals"))
		for _, e := range result.Skipped {
			appendix.WriteString(paragraph(fallback(str(e, "title"), str(e, "ref")) + ": " + str(e, "export_reason")))
			appendix.WriteString(paragraph("Original: " + str(e, "original_text")))
			appendix.WriteString(paragraph("Proposed: " + str(e, "proposed_text")))
		}
	}
	if len(includeNotice) > 0 && includeNotice[0] {
		appendix.WriteString(paragraph(aiReviewNotice))
	}
	if appendix.Len() > 0 {
		var newBody *wordNode
		walkWord(check, func(n *wordNode) {
			if isWord(n, "body") {
				newBody = n
			}
		})
		at := newBody.CloseStart
		for _, n := range newBody.Children {
			if isWord(n, "sectPr") {
				at = n.Start
				break
			}
		}
		patched = append(append(append([]byte{}, patched[:at]...), []byte(appendix.String())...), patched[at:]...)
	}
	var output bytes.Buffer
	writer := zip.NewWriter(&output)
	for _, f := range z.File {
		if f.Name != "word/document.xml" {
			if err = writer.Copy(f); err != nil {
				return result, err
			}
			continue
		}
		header := f.FileHeader
		w, err := writer.CreateHeader(&header)
		if err != nil {
			return result, err
		}
		if _, err = w.Write(patched); err != nil {
			return result, err
		}
	}
	if err = writer.Close(); err != nil {
		return result, err
	}
	result.Content = output.Bytes()
	return result, nil
}
