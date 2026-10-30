package app

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

//go:embed courts.json
var jurisdictionCourtsJSON []byte

func (s *Server) legalDocumentRoutes() {
	s.api("GET /api/v1/legal-docs/courts", false, func(q request) (any, error) {
		var courts []M
		if err := json.Unmarshal(jurisdictionCourtsJSON, &courts); err != nil {
			return nil, err
		}
		return M{"courts": courts, "count": len(courts)}, nil
	})
	s.api("POST /api/v1/legal-docs/extract-text", false, s.extractUploadedText)
	s.api("POST /api/v1/legal-docs/convert-to-pdf", false, s.convertUploadedPDF)
}

func (s *Server) readFileToolUpload(q request) ([]byte, string, func(), error) {
	select {
	case s.uploads <- struct{}{}:
	default:
		return nil, "", nil, fail(429, "Two uploads are in progress. Please wait and retry.")
	}
	cleanup := func() {
		if q.r.MultipartForm != nil {
			_ = q.r.MultipartForm.RemoveAll()
		}
		<-s.uploads
	}
	q.r.Body = http.MaxBytesReader(q.w, q.r.Body, s.cfg.MaxUpload+(1<<20))
	if err := q.r.ParseMultipartForm(1 << 20); err != nil {
		cleanup()
		var sizeError *http.MaxBytesError
		if errors.As(err, &sizeError) {
			return nil, "", nil, fail(413, "File exceeds the upload limit")
		}
		return nil, "", nil, bad("Invalid file upload")
	}
	file, header, err := q.r.FormFile("file")
	if err != nil {
		cleanup()
		return nil, "", nil, fail(422, "A file is required")
	}
	defer file.Close()
	name := filepath.Base(strings.ReplaceAll(header.Filename, "\\", "/"))
	if name == "." || name == "" || len(name) > 255 {
		cleanup()
		return nil, "", nil, bad("Invalid filename")
	}
	data, err := io.ReadAll(io.LimitReader(file, s.cfg.MaxUpload+1))
	if err != nil {
		cleanup()
		return nil, "", nil, err
	}
	if int64(len(data)) > s.cfg.MaxUpload {
		cleanup()
		return nil, "", nil, fail(413, "File exceeds the upload limit")
	}
	if len(data) == 0 {
		cleanup()
		return nil, "", nil, fail(422, "The uploaded file is empty")
	}
	if _, err = validateDocumentUpload(data, name); err != nil {
		cleanup()
		return nil, "", nil, err
	}
	return data, name, cleanup, nil
}

type fileToolText struct {
	nativeDocument
	OCR bool
}

func (s *Server) fileToolExtraction(q request, data []byte, name string) (fileToolText, error) {
	out := fileToolText{}
	if bytes.HasPrefix(bytes.TrimSpace(data), []byte("%PDF-")) {
		pages, err := nativePDFPages(data)
		var limit *apiError
		if errors.As(err, &limit) && limit.status == 422 {
			return out, err
		}
		if err == nil && assembleText(pages).Complete {
			result := assembleText(pages)
			out.Text = result.Text
			for _, p := range pages {
				out.Pages = append(out.Pages, p.Text)
				out.Tables = append(out.Tables, p.Tables...)
			}
			return out, nil
		}
		if s.ocr != nil {
			select {
			case s.ocr <- struct{}{}:
				defer func() { <-s.ocr }()
			case <-q.r.Context().Done():
				return out, q.r.Context().Err()
			}
		}
		if len(pages) == 0 {
			result, e := s.gm.OCR(q.r.Context(), data, "PDF", nil, nil)
			if e != nil {
				return out, e
			}
			if e = result.fullTextError(); e != nil {
				return out, e
			}
			pages = result.Pages
		} else {
			textBytes := max(0, len(pages)-1) * 2
			for _, p := range pages {
				textBytes += len(p.Text)
			}
			for first := 0; first < len(pages); {
				if !pages[first].Failed {
					first++
					continue
				}
				last := first
				for last+1 < len(pages) && last-first < 9 && pages[last+1].Failed {
					last++
				}
				part, e := s.gm.OCR(q.r.Context(), data, "PDF", &first, &last)
				if e != nil {
					return out, e
				}
				if !part.Complete {
					return out, part.fullTextError()
				}
				for _, p := range part.Pages {
					textBytes += len(p.Text) - len(pages[p.Index].Text)
					if textBytes > maxExtractedText {
						return out, fail(422, "Extracted text exceeds the 4 MB document limit")
					}
					pages[p.Index] = p
				}
				first = last + 1
			}
		}
		result := assembleText(pages)
		if err = result.fullTextError(); err != nil {
			return out, err
		}
		out.Text = result.Text
		out.OCR = true
		for _, p := range pages {
			out.Pages = append(out.Pages, p.Text)
			out.Tables = append(out.Tables, p.Tables...)
		}
	} else {
		var err error
		out.nativeDocument, err = extractNativeDocument(data, name)
		if err != nil {
			return out, fail(422, err.Error())
		}
	}
	if len(out.Text) > maxExtractedText {
		return out, fail(422, "Extracted text exceeds the 4 MB document limit")
	}
	if strings.TrimSpace(out.Text) == "" {
		return out, fail(422, "No readable text was found in this document")
	}
	return out, nil
}
func (s *Server) extractUploadedText(q request) (any, error) {
	data, name, cleanup, err := s.readFileToolUpload(q)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	result, err := s.fileToolExtraction(q, data, name)
	if err != nil {
		return nil, err
	}
	return M{"success": true, "filename": name, "text": result.Text, "char_count": utf8.RuneCountInString(result.Text), "word_count": len(strings.Fields(result.Text)), "page_count": len(result.Pages), "tables_found": len(result.Tables), "ocr_used": result.OCR, "truncated": false, "warnings": []string{}}, nil
}
func (s *Server) convertUploadedPDF(q request) (any, error) {
	data, name, cleanup, err := s.readFileToolUpload(q)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	if !strings.EqualFold(filepath.Ext(name), ".pdf") {
		result, err := s.fileToolExtraction(q, data, name)
		if err != nil {
			return nil, err
		}
		blocks := result.Blocks
		if len(blocks) == 0 {
			for _, line := range strings.Split(result.Text, "\n") {
				kind := "text"
				if strings.HasPrefix(line, "## ") {
					kind = "heading"
					line = strings.TrimPrefix(line, "## ")
				}
				blocks = append(blocks, reportBlock{Kind: kind, Text: line})
			}
		}
		data, err = conversationPDF(q.r.Context(), blocks)
		if err != nil {
			return nil, err
		}
	}
	q.w.Header().Set("Content-Type", "application/pdf")
	q.w.Header().Set("Content-Disposition", `inline; filename="document.pdf"`)
	q.w.Header().Set("Cache-Control", "no-store")
	_, err = q.w.Write(data)
	return nil, err
}
