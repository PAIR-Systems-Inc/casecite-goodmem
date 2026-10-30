package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

type textPage struct {
	Index  int      `json:"page_index"`
	Text   string   `json:"text"`
	Method string   `json:"method"`
	Failed bool     `json:"failed,omitempty"`
	Start  int      `json:"span_start"`
	End    int      `json:"span_end"`
	Tables []string `json:"tables,omitempty"`
}
type extractedDocument struct {
	Text        string     `json:"text"`
	Method      string     `json:"method"`
	Complete    bool       `json:"complete"`
	Pages       []textPage `json:"pages"`
	FailedPages []int      `json:"failed_pages"`
	SHA256      string     `json:"content_sha256,omitempty"`
}

func assembleText(pages []textPage) extractedDocument {
	sort.SliceStable(pages, func(i, j int) bool { return pages[i].Index < pages[j].Index })
	out := extractedDocument{Complete: true, Pages: pages, FailedPages: []int{}, Method: "native"}
	var text strings.Builder
	offset := 0
	hasNative, hasOCR := false, false
	for i := range out.Pages {
		page := &out.Pages[i]
		if i > 0 {
			text.WriteString("\n\n")
			offset += 2
		}
		page.Start = offset
		text.WriteString(page.Text)
		offset += byteToUTF16(page.Text, len(page.Text))
		page.End = offset
		if page.Failed {
			out.Complete = false
			out.FailedPages = append(out.FailedPages, page.Index)
		}
		hasOCR = hasOCR || page.Method == "goodmem_ocr"
		hasNative = hasNative || page.Method == "native"
	}
	if hasOCR {
		out.Method = "goodmem_ocr"
		if hasNative {
			out.Method = "native_and_goodmem_ocr"
		}
	}
	out.Text = text.String()
	return out
}

// GoodMem owns rendering and OCR. CaseCite only calls its public OCR service,
// validates complete page coverage, and caches the response for repeated reads.
// This endpoint is not idempotent: do not transparently retry a failed request.
func (g *GoodMem) OCR(ctx context.Context, content []byte, format string, start, end *int) (extractedDocument, error) {
	if (start != nil && *start < 0) || (end != nil && (*end < 0 || start != nil && *end < *start)) {
		return extractedDocument{}, fail(422, "Invalid OCR page range")
	}
	in := M{"content": content, "format": format, "includeRawJson": false, "includeMarkdown": true}
	if start != nil {
		in["startPage"] = *start
	}
	if end != nil {
		in["endPage"] = *end
	}
	timeout := g.OCRTimeout
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST", g.URL+"/v1/ocr:document", bytes.NewReader(jsonBytes(in)))
	if err != nil {
		return extractedDocument{}, err
	}
	req.Header.Set("x-api-key", g.Key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	client := *g.Client
	client.Timeout = timeout
	resp, err := client.Do(req)
	if err != nil {
		return extractedDocument{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		switch resp.StatusCode {
		case 401, 403:
			return extractedDocument{}, fail(503, "GoodMem OCR requires an API key with OCR_DOCUMENT permission. Ask the administrator to update the GoodMem connection.")
		case 404:
			return extractedDocument{}, fail(503, "This GoodMem installation does not expose the OCR API. Enable or update its OCR service.")
		case 400, 413, 422:
			return extractedDocument{}, fail(422, "GoodMem could not OCR this document. Check its format and the GoodMem page/size limits.")
		default:
			return extractedDocument{}, fail(502, "GoodMem OCR is unavailable. Check the on-premise OCR backend and retry.")
		}
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (16<<20)+1))
	if err != nil {
		return extractedDocument{}, err
	}
	if len(raw) > 16<<20 {
		return extractedDocument{}, fail(422, "GoodMem OCR output exceeds the 16 MB response limit")
	}
	var response struct {
		PageCount int `json:"pageCount"`
		Pages     []struct {
			Index *int `json:"pageIndex"`
			Page  *struct {
				Markdown *string `json:"markdown"`
				Layout   *struct {
					Cells []struct {
						Text     string `json:"text"`
						Category any    `json:"category"`
					} `json:"cells"`
				} `json:"layout"`
			} `json:"page"`
			Status *struct {
				Code int `json:"code"`
			} `json:"status"`
		} `json:"pages"`
	}
	if err = json.Unmarshal(raw, &response); err != nil {
		return extractedDocument{}, fail(502, "GoodMem returned an invalid OCR response")
	}
	first := 0
	if start != nil {
		first = *start
	}
	count := response.PageCount
	if end != nil {
		count = *end - first + 1
	}
	if count <= 0 || count > 10000 || response.PageCount != count {
		return extractedDocument{}, fail(502, "GoodMem returned an incomplete OCR page count")
	}
	pages := make([]textPage, count)
	for i := range pages {
		pages[i] = textPage{Index: first + i, Method: "goodmem_ocr", Failed: true}
	}
	seen := map[int]bool{}
	for _, result := range response.Pages {
		if result.Index == nil || *result.Index < first || *result.Index >= first+count || seen[*result.Index] {
			return extractedDocument{}, fail(502, "GoodMem returned duplicate or invalid OCR page indices")
		}
		seen[*result.Index] = true
		if result.Status != nil || result.Page == nil {
			continue
		}
		page := textPage{Index: *result.Index, Method: "goodmem_ocr"}
		// Layout cells are already in reading order. Normalize their HTML tables
		// before offsets/indexing; retain every other cell's source text rather
		// than accepting a different page-level Markdown rendering.
		if result.Page.Layout != nil {
			parts := []string{}
			for _, cell := range result.Page.Layout.Cells {
				if strings.TrimSpace(cell.Text) != "" {
					if cell.Category == "OCR_CATEGORY_TABLE" || cell.Category == "TABLE" || cell.Category == float64(9) {
						cell.Text = normalizeOCRTable(cell.Text)
						page.Tables = append(page.Tables, cell.Text)
					}
					parts = append(parts, cell.Text)
				}
			}
			page.Text = strings.Join(parts, "\n")
			if page.Text == "" && result.Page.Markdown != nil {
				page.Text = *result.Page.Markdown
			}
		} else if result.Page.Markdown != nil {
			page.Text = *result.Page.Markdown
		} else {
			page.Failed = true
		}
		pages[*result.Index-first] = page
	}
	out := assembleText(pages)
	if len(out.Text) > 4<<20 {
		return extractedDocument{}, fail(422, "OCR text exceeds the 4 MB document limit")
	}
	return out, nil
}
func (d extractedDocument) fullTextError() error {
	if !d.Complete {
		pages := []string{}
		for _, index := range d.FailedPages {
			pages = append(pages, fmt.Sprint(index+1))
		}
		return fail(502, "GoodMem OCR did not complete pages "+strings.Join(pages, ", ")+". Full-document analysis requires every page. Check the on-premise OCR backend and retry.")
	}
	if strings.TrimSpace(d.Text) == "" {
		return fail(422, "No readable text was found in this document, including GoodMem OCR")
	}
	return nil
}

var errNoDocumentImages = errors.New("GoodMem has not produced page images for this format")
