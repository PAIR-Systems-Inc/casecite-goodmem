package app

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"unicode"

	"github.com/signintech/gopdf"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goitalic"
	"golang.org/x/image/font/gofont/goregular"
)

// Fonts are compiled into the Go server. Export needs neither Python nor a
// browser/office process, and never downloads fonts or sends text elsewhere.
func conversationPDF(ctx context.Context, blocks []reportBlock) ([]byte, error) {
	var pdf gopdf.GoPdf
	pdf.Start(gopdf.Config{PageSize: *gopdf.PageSizeLetter})
	for family, data := range map[string][]byte{"body": goregular.TTF, "bold": gobold.TTF, "italic": goitalic.TTF} {
		if err := pdf.AddTTFFontByReader(family, bytes.NewReader(data)); err != nil {
			return nil, err
		}
	}
	y, pages := 72.0, 0
	page := func() error {
		pages++
		pdf.AddPage()
		if err := pdf.SetFont("body", "", 9); err != nil {
			return err
		}
		pdf.SetXY(288, 750)
		if err := pdf.Text(fmt.Sprintf("%d", pages)); err != nil {
			return err
		}
		y = 72
		return nil
	}
	if err := page(); err != nil {
		return nil, err
	}
	previous, ordinal := "", 0
	glyphs := map[string]map[rune]bool{}
	for _, block := range blocks {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		family, size, leading, before, after, indent := "body", 11.0, 15.0, 0.0, 5.0, 0.0
		heading := false
		switch block.Kind {
		case "title":
			family, size, leading, after, heading = "bold", 18, 22, 4, true
		case "heading":
			family, size, leading, before, after, heading = "bold", 12.5, 16, 12, 4, true
		case "subheading":
			family, size, leading, before, after, heading = "bold", 11.5, 15, 8, 3, true
		case "meta":
			family = "bold"
			heading = true
		case "stamp":
			family, size, leading, after = "italic", 9, 13, 10
		case "quote":
			family, size, leading, indent = "italic", 9.5, 13, 18
		case "notice":
			family, before = "italic", 16
		case "ordered", "unordered":
			indent = 16
		}
		if err := pdf.SetFont(family, "", size); err != nil {
			return nil, err
		}
		text := strings.ReplaceAll(block.Text, "\t", "    ")
		text = strings.Map(func(r rune) rune {
			if unicode.IsControl(r) && r != '\n' {
				return ' '
			}
			return r
		}, text)
		if strings.TrimSpace(text) == "" {
			continue
		}
		if glyphs[family] == nil {
			glyphs[family] = map[rune]bool{}
		}
		for _, r := range text {
			if r == '\n' || glyphs[family][r] {
				continue
			}
			present, err := pdf.IsCurrFontContainGlyph(r)
			if err != nil {
				return nil, err
			}
			if !present {
				return nil, fail(422, fmt.Sprintf("The embedded PDF font cannot display Unicode U+%04X. Export as Word or Markdown to preserve all characters.", r))
			}
			glyphs[family][r] = true
		}
		lines, err := pdf.SplitTextWithWordWrap(text, 468-indent)
		if err != nil {
			return nil, err
		}
		needed := leading * float64(min(2, len(lines)))
		if heading {
			needed = leading*float64(min(3, len(lines))) + 20
		}
		if y+before+needed > 720 {
			if err := page(); err != nil {
				return nil, err
			}
			if err := pdf.SetFont(family, "", size); err != nil {
				return nil, err
			}
			before = 0
		}
		y += before
		if block.Kind == "ordered" {
			if previous != "ordered" {
				ordinal = 0
			}
			ordinal++
		}
		for i, line := range lines {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if y+leading > 720 || len(lines)-i == 2 && y+2*leading > 720 {
				if err := page(); err != nil {
					return nil, err
				}
				if err := pdf.SetFont(family, "", size); err != nil {
					return nil, err
				}
			}
			if i == 0 && (block.Kind == "ordered" || block.Kind == "unordered") {
				bullet := "•"
				if block.Kind == "ordered" {
					bullet = fmt.Sprintf("%d.", ordinal)
				}
				pdf.SetXY(72, y+size)
				if err := pdf.Text(bullet); err != nil {
					return nil, err
				}
			}
			pdf.SetXY(72+indent, y+size)
			if err := pdf.Text(line); err != nil {
				return nil, err
			}
			y += leading
		}
		y += after
		previous = block.Kind
	}
	return pdf.GetBytesPdfReturnErr()
}
