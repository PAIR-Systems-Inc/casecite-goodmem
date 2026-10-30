package app

import (
	"bytes"
	"encoding/base64"
	"encoding/xml"
	"io"
	"net/http"
	"regexp"
	"strings"
)

func defaultBranding() M {
	return M{"firm_name": "CaseCite", "logo_url": nil, "primary_color": "#E5E5E5", "secondary_color": "#0A0A0A", "accent_color": "#737373", "favicon_url": nil, "custom_css": nil, "updated_at": nil}
}

var cssDangerous = regexp.MustCompile(`(?i)javascript:|expression\s*\(|@import\b|(?:-webkit-)?image-set\s*\(`)
var cssURL = regexp.MustCompile(`(?is)url\s*\(\s*(.*?)\s*\)`)
var rasterData = regexp.MustCompile(`(?i)^data:image/(png|jpeg|gif|webp);base64,[a-z0-9+/=\s]+$`)
var brandColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
var brandAssetName = regexp.MustCompile(`^brand_logo_[0-9a-f]{32}\.(png|jpg|svg|webp)$`)

func sanitizeCSS(css string) string {
	css = strings.NewReplacer("<", "", ">", "").Replace(css)
	for {
		before := css
		css = cssDangerous.ReplaceAllString(css, "")
		css = cssURL.ReplaceAllStringFunc(css, func(raw string) string {
			matches := cssURL.FindStringSubmatch(raw)
			target := strings.Trim(strings.TrimSpace(matches[1]), "\"'")
			target = strings.TrimSpace(target)
			if !strings.Contains(target, "\\") && ((strings.HasPrefix(target, "/") && !strings.HasPrefix(target, "//")) || rasterData.MatchString(target)) {
				return raw
			}
			return "none"
		})
		if css == before {
			return css
		}
	}
}

func (s *Server) branding(q request) (any, error) {
	m := defaultBranding()
	stored := M{}
	err := s.kvGet(q.r.Context(), "branding", &stored)
	if err != nil && !notFound(err) {
		return nil, err
	}
	for k, v := range stored {
		m[k] = v
	}
	if q.r.Method == "GET" {
		return m, nil
	}
	if err = permit(q, "admin.settings"); err != nil {
		return nil, err
	}
	if q.r.Method == "POST" {
		m = defaultBranding()
	} else {
		in, err := body(q)
		if err != nil {
			return nil, err
		}
		for k, v := range in {
			if _, ok := m[k]; !ok || k == "updated_at" {
				continue
			}
			if v == nil {
				if k == "logo_url" || k == "favicon_url" || k == "custom_css" {
					m[k] = nil
				}
				continue
			}
			value, ok := v.(string)
			if !ok {
				return nil, bad("Branding values must be text")
			}
			switch k {
			case "firm_name":
				if len(value) > 200 || strings.TrimSpace(value) == "" {
					return nil, bad("Firm name is required and must not exceed 200 characters")
				}
			case "primary_color", "secondary_color", "accent_color":
				if !brandColor.MatchString(value) {
					return nil, bad("Colors must use #RRGGBB")
				}
			case "logo_url", "favicon_url":
				if value != "" && !(strings.HasPrefix(value, "/") && !strings.HasPrefix(value, "//") && !strings.Contains(value, "\\")) && !(strings.HasPrefix(value, "https://") && safeHTTPURL(value)) {
					return nil, bad("Branding URLs must be same-origin paths or HTTPS URLs")
				}
			case "custom_css":
				if len(value) > 50000 {
					return nil, bad("Custom CSS is too large")
				}
				value = sanitizeCSS(value)
			}
			m[k] = value
		}
	}
	m["updated_at"] = nowString()
	if err = s.kvSet(q.r.Context(), "branding", m, 0); err != nil {
		return nil, err
	}
	s.audit(q.r.Context(), q.user.ID, "branding.update", "branding", "", M{})
	return m, nil
}

func validateSVG(b []byte) error {
	d := xml.NewDecoder(bytes.NewReader(b))
	root := false
	inStyle := false
	for {
		tok, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return bad("Invalid SVG image")
		}
		switch t := tok.(type) {
		case xml.Directive:
			return bad("SVG directives are not allowed")
		case xml.ProcInst:
			if t.Target != "xml" {
				return bad("SVG processing instructions are not allowed")
			}
		case xml.StartElement:
			name := strings.ToLower(t.Name.Local)
			if !root {
				if name != "svg" {
					return bad("Not an SVG image")
				}
				root = true
			}
			if name == "script" || name == "foreignobject" || name == "iframe" || name == "embed" || name == "object" || name == "animate" || name == "set" {
				return bad("SVG contains active content")
			}
			inStyle = name == "style"
			for _, a := range t.Attr {
				key := strings.ToLower(a.Name.Local)
				value := strings.TrimSpace(a.Value)
				if strings.HasPrefix(key, "on") {
					return bad("SVG event handlers are not allowed")
				}
				if key == "href" && !strings.HasPrefix(value, "#") && !rasterData.MatchString(value) {
					return bad("SVG external resources are not allowed")
				}
				if (key == "style" || strings.Contains(strings.ToLower(value), "url(")) && sanitizeCSS(value) != value {
					return bad("SVG contains unsafe CSS")
				}
			}
		case xml.EndElement:
			if t.Name.Local == "style" {
				inStyle = false
			}
		case xml.CharData:
			if inStyle && sanitizeCSS(string(t)) != string(t) {
				return bad("SVG contains unsafe CSS")
			}
		}
	}
	if !root {
		return bad("Not an SVG image")
	}
	return nil
}

func (s *Server) brandingLogo(q request) (any, error) {
	if err := permit(q, "admin.settings"); err != nil {
		return nil, err
	}
	q.r.Body = http.MaxBytesReader(q.w, q.r.Body, (2<<20)+(64<<10))
	if err := q.r.ParseMultipartForm(2 << 20); err != nil {
		return nil, bad("Logo must not exceed 2 MB")
	}
	defer q.r.MultipartForm.RemoveAll()
	file, header, err := q.r.FormFile("file")
	if err != nil {
		return nil, bad("Choose a logo file")
	}
	defer file.Close()
	b, err := io.ReadAll(io.LimitReader(file, (2<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(b) > 2<<20 {
		return nil, bad("Logo must not exceed 2 MB")
	}
	ct := header.Header.Get("Content-Type")
	ext := ""
	switch ct {
	case "image/png":
		if bytes.HasPrefix(b, []byte("\x89PNG\r\n\x1a\n")) {
			ext = "png"
		}
	case "image/jpeg":
		if bytes.HasPrefix(b, []byte("\xff\xd8\xff")) {
			ext = "jpg"
		}
	case "image/webp":
		if len(b) >= 12 && string(b[:4]) == "RIFF" && string(b[8:12]) == "WEBP" {
			ext = "webp"
		}
	case "image/svg+xml":
		if err = validateSVG(b); err != nil {
			return nil, err
		}
		ext = "svg"
	}
	if ext == "" {
		return nil, bad("Choose a valid PNG, JPEG, SVG or WebP image")
	}
	name := "brand_logo_" + strings.ReplaceAll(uuid(), "-", "") + "." + ext
	url := "/api/v1/branding/assets/" + name
	if err = s.kvSet(q.r.Context(), "branding-asset:"+name, M{"content_type": ct, "content": base64.StdEncoding.EncodeToString(b)}, 0); err != nil {
		return nil, err
	}
	m := defaultBranding()
	if err = s.kvGet(q.r.Context(), "branding", &m); err != nil && !notFound(err) {
		return nil, err
	}
	m["logo_url"] = url
	m["updated_at"] = nowString()
	if err = s.kvSet(q.r.Context(), "branding", m, 0); err != nil {
		return nil, err
	}
	s.audit(q.r.Context(), q.user.ID, "branding.logo", "branding", "", M{})
	return M{"status": "uploaded", "logo_url": url}, nil
}

func (s *Server) brandingAsset(q request) (any, error) {
	name := q.r.PathValue("filename")
	if !brandAssetName.MatchString(name) {
		return nil, missing()
	}
	var m M
	if err := s.kvGet(q.r.Context(), "branding-asset:"+name, &m); err != nil {
		return nil, err
	}
	b, err := base64.StdEncoding.DecodeString(str(m, "content"))
	if err != nil {
		return nil, err
	}
	q.w.Header().Set("Content-Type", str(m, "content_type"))
	q.w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	q.w.Header().Set("Cache-Control", "public, max-age=86400")
	_, err = q.w.Write(b)
	return nil, err
}
