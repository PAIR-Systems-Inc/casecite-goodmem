package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/text/unicode/norm"
)

var judgeNameTokenRE = regexp.MustCompile(`[a-z]+`)
var judgeTitleSuffixRE = regexp.MustCompile(`\s*\([^)]*\)\s*$`)
var judgeJudicialRE = regexp.MustCompile(`(?i)\b(judge|justice|jurist|magistrate|judiciary)\b`)
var judgeBirthYearRE = regexp.MustCompile(`\b(1[6-9]\d{2}|20\d{2})\b`)
var judgeParensRE = regexp.MustCompile(`\(([^)]*)\)`)
var judgeHeadingRE = regexp.MustCompile(`^={2,}\s*(.+?)\s*={2,}$`)

func judgeNameTokens(text string) []string {
	var ascii strings.Builder
	for _, r := range norm.NFKD.String(text) {
		if r < 128 {
			ascii.WriteRune(r)
		}
	}
	return judgeNameTokenRE.FindAllString(strings.ToLower(ascii.String()), -1)
}
func judgeWikipediaTitle(title, name string) bool {
	words := judgeNameTokens(name)
	// A full CourtListener name may include Jr./Sr.; these are not surnames.
	for len(words) > 2 && slices.Contains([]string{"jr", "sr", "ii", "iii", "iv"}, words[len(words)-1]) {
		words = words[:len(words)-1]
	}
	if len(words) < 2 {
		return false
	}
	page := judgeNameTokens(judgeTitleSuffixRE.ReplaceAllString(title, ""))
	first, last := words[0], words[len(words)-1]
	if !slices.Contains(page, last) {
		return false
	}
	for _, word := range page {
		if word != last && (word == first || len(word) == 1 && strings.HasPrefix(first, word) || len(first) == 1 && strings.HasPrefix(word, first)) {
			return true
		}
	}
	return false
}
func judgeWikipediaIntro(intro string, birthYear int) bool {
	if !judgeJudicialRE.MatchString(charSlice(intro, 0, 1500)) {
		return false
	}
	if birthYear != 0 {
		if parens := judgeParensRE.FindStringSubmatch(charSlice(intro, 0, 400)); len(parens) > 1 {
			if year := judgeBirthYearRE.FindString(parens[1]); year != "" && year != strconv.Itoa(birthYear) {
				return false
			}
		}
	}
	return true
}
func judgeWikiSections(text string) M {
	patterns := map[string]string{"early_life": "early life|early years|childhood|biography|background", "education": "education|academic|school|university|college", "career": "career|legal career|professional|practice|work", "judicial_service": "judicial|judge|court|bench|nomination|confirmation|service", "notable_cases": "notable|significant|important|landmark|cases|decisions|rulings|opinions", "personal_life": "personal life|family|marriage|spouse|children|private"}
	sections := map[string][]string{"intro": {}}
	keys := []string{"intro"}
	current := "intro"
	for _, line := range strings.Split(text, "\n") {
		if m := judgeHeadingRE.FindStringSubmatch(strings.TrimSpace(line)); len(m) > 1 {
			current = strings.ToLower(strings.TrimSpace(m[1]))
			if _, ok := sections[current]; !ok {
				keys = append(keys, current)
			}
			sections[current] = []string{}
		} else {
			sections[current] = append(sections[current], line)
		}
	}
	out := M{}
	for key, pattern := range patterns {
		re := regexp.MustCompile("(?i)" + pattern)
		for _, name := range keys {
			if re.MatchString(name) {
				out[key] = strings.TrimSpace(strings.Join(sections[name], "\n"))
				break
			}
		}
	}
	if len(out) == 0 {
		for key, pattern := range patterns {
			if m := regexp.MustCompile("(?i)" + pattern).FindStringIndex(text); len(m) > 0 {
				start := len([]rune(text[:m[0]]))
				end := len([]rune(text[:m[1]]))
				out[key] = strings.TrimSpace(charSlice(text, max(0, start-50), end+2000))
			}
		}
	}
	return out
}
func (s *Server) judgeWikiJSON(ctx context.Context, params url.Values) (M, error) {
	u, err := url.Parse(fallback(s.cfg.WikipediaURL, "https://en.wikipedia.org/w/api.php"))
	if err != nil {
		return nil, err
	}
	u.RawQuery = params.Encode()
	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "CaseCite-GoodMem/1.0 (https://github.com/PAIR-Systems-Inc/casecite-goodmem)")
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fail(502, "Wikipedia biography is unavailable")
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (4<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > 4<<20 {
		return nil, fail(502, "Wikipedia response is too large")
	}
	out := M{}
	err = json.Unmarshal(raw, &out)
	return out, err
}
func judgeWikiExtract(data M) string {
	pages := object(object(data["query"])["pages"])
	keys := []string{}
	for k := range pages {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		if k != "-1" {
			return str(object(pages[k]), "extract")
		}
	}
	return ""
}
func (s *Server) judgeWikipedia(ctx context.Context, name string, birthYear int) M {
	out := M{}
	for _, key := range []string{"url", "summary", "bio", "early_life", "education", "career", "judicial_service", "notable_cases", "personal_life", "raw"} {
		out[key] = nil
	}
	ctx, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	params := url.Values{"action": {"query"}, "list": {"search"}, "srsearch": {strings.TrimSpace(name) + " judge"}, "srlimit": {"5"}, "format": {"json"}}
	data, err := s.judgeWikiJSON(ctx, params)
	if err != nil {
		return out
	}
	results := maps(object(data["query"])["search"])
	if len(results) == 0 {
		params.Set("srsearch", strings.TrimSpace(name))
		data, err = s.judgeWikiJSON(ctx, params)
		if err != nil {
			return out
		}
		results = maps(object(data["query"])["search"])
	}
	candidates := 0
	for _, result := range results {
		title := str(result, "title")
		if !judgeWikipediaTitle(title, name) {
			continue
		}
		candidates++
		if candidates > 3 {
			break
		}
		params = url.Values{"action": {"query"}, "titles": {title}, "prop": {"extracts"}, "exintro": {"true"}, "explaintext": {"true"}, "format": {"json"}}
		data, err = s.judgeWikiJSON(ctx, params)
		if err != nil {
			continue
		}
		intro := judgeWikiExtract(data)
		if !judgeWikipediaIntro(intro, birthYear) {
			continue
		}
		out["url"] = "https://en.wikipedia.org/wiki/" + url.PathEscape(strings.ReplaceAll(title, " ", "_"))
		out["summary"] = intro
		params.Del("exintro")
		data, err = s.judgeWikiJSON(ctx, params)
		if err == nil {
			text := charSlice(judgeWikiExtract(data), 0, 100000)
			out["raw"], out["bio"] = text, nullable(charSlice(text, 0, 5000))
			for k, v := range judgeWikiSections(text) {
				out[k] = v
			}
		}
		break
	}
	return out
}
