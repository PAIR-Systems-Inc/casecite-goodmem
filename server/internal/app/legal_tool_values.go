package app

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"html"
	"math"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Extracted from the pinned Python source by scripts/parity/legal_tool_fixtures.py.
//
//go:embed legal_tool_assets.json
var legalToolAssetsJSON []byte

var legalToolAssets = func() struct {
	OpinionTypes  map[string]string   `json:"opinion_types"`
	Jurisdictions map[string][]string `json:"jurisdictions"`
	ReporterRanks []struct {
		Pattern string
		Rank    int
		re      *regexp.Regexp
	} `json:"reporter_ranks"`
} {
	var out struct {
		OpinionTypes  map[string]string   `json:"opinion_types"`
		Jurisdictions map[string][]string `json:"jurisdictions"`
		ReporterRanks []struct {
			Pattern string
			Rank    int
			re      *regexp.Regexp
		} `json:"reporter_ranks"`
	}
	if err := json.Unmarshal(legalToolAssetsJSON, &out); err != nil {
		panic(err)
	}
	for i := range out.ReporterRanks {
		out.ReporterRanks[i].re = regexp.MustCompile(out.ReporterRanks[i].Pattern)
	}
	return out
}()

func legalStripMarks(value string) string {
	return strings.Join(strings.Fields(html.UnescapeString(stripTags.ReplaceAllString(value, ""))), " ")
}
func legalNestedSnippet(result M) string {
	if values, ok := result["opinions"].([]any); ok && len(values) > 0 {
		if first, ok := values[0].(M); ok {
			return legalStripMarks(str(first, "snippet"))
		}
	}
	if values, ok := result["opinions"].([]M); ok && len(values) > 0 {
		return legalStripMarks(str(values[0], "snippet"))
	}
	return legalStripMarks(str(result, "snippet"))
}
func legalFirstOpinion(result M) any {
	if values, ok := result["opinions"].([]any); ok && len(values) > 0 {
		return object(values[0])["id"]
	}
	if values, ok := result["opinions"].([]M); ok && len(values) > 0 {
		return values[0]["id"]
	}
	return nil
}
func legalCitationRank(cite string) int {
	ranks := legalToolAssets.ReporterRanks
	if ranks[len(ranks)-1].re.MatchString(cite) {
		return ranks[len(ranks)-1].Rank
	}
	for _, r := range ranks[:len(ranks)-1] {
		if r.re.MatchString(cite) {
			return r.Rank
		}
	}
	return 6
}
func legalOrderCitations(raw any) []string {
	values := []string{}
	switch list := raw.(type) {
	case []string:
		values = append(values, list...)
	case []any:
		for _, value := range list {
			if text := legalScalar(value); text != "" {
				values = append(values, text)
			}
		}
	}
	out := []string{}
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	slices.SortStableFunc(out, func(a, b string) int { return legalCitationRank(a) - legalCitationRank(b) })
	return out
}
func legalScalar(value any) string {
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	case float64:
		if v != 0 && !math.IsInf(v, 0) && !math.IsNaN(v) {
			return strconv.FormatFloat(v, 'f', -1, 64)
		}
	case int:
		if v != 0 {
			return strconv.Itoa(v)
		}
	case bool:
		if v {
			return "True"
		}
	}
	return ""
}
func legalCitationString(value any) any {
	if v, ok := value.(string); ok {
		return nullable(strings.TrimSpace(v))
	}
	parts := []string{}
	for _, key := range []string{"volume", "reporter", "page"} {
		if value := legalScalar(object(value)[key]); value != "" {
			parts = append(parts, value)
		}
	}
	return nullable(strings.Join(parts, " "))
}
func legalDuration(value any) any {
	var total int
	switch v := value.(type) {
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) || v > float64(math.MaxInt) {
			return nil
		}
		total = int(v)
	case int:
		total = v
	case string:
		var err error
		total, err = strconv.Atoi(strings.TrimSpace(v))
		if err != nil {
			return nil
		}
	default:
		return nil
	}
	if total <= 0 {
		return nil
	}
	if total >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", total/3600, total/60%60, total%60)
	}
	return fmt.Sprintf("%d:%02d", total/60, total%60)
}
func legalJurisdiction(value string) []string {
	return append([]string{}, legalToolAssets.Jurisdictions[strings.ToLower(value)]...)
}

var legalCircuitRE = regexp.MustCompile(`^(\d{1,2})(?:st|nd|rd|th)?(?:[\s_.-]*cir(?:cuit|\.)?)?$`)
var legalCourtIDRE = regexp.MustCompile(`^[a-z][a-z0-9]{1,15}$`)
var legalJurisdictionSpaceRE = regexp.MustCompile(`[\s-]+`)

func legalResolveCourts(values []string) []string {
	out := []string{}
	for _, raw := range values {
		value := strings.ToLower(strings.TrimSpace(raw))
		key := legalJurisdictionSpaceRE.ReplaceAllString(value, "_")
		mapped := legalJurisdiction(key)
		if len(mapped) == 0 {
			number := 0
			if match := legalCircuitRE.FindStringSubmatch(value); match != nil {
				number, _ = strconv.Atoi(match[1])
			} else {
				word := strings.TrimSuffix(strings.TrimSuffix(key, "_circuit"), "_cir")
				number = slices.Index(strings.Fields("first second third fourth fifth sixth seventh eighth ninth tenth eleventh"), word) + 1
			}
			switch {
			case number >= 1 && number <= 11:
				mapped = []string{fmt.Sprintf("ca%d", number)}
			case slices.Contains([]string{"dc", "d.c.", "dc_cir", "d.c._cir."}, value):
				mapped = []string{"cadc"}
			case legalCourtIDRE.MatchString(value):
				mapped = []string{value}
			}
		}
		for _, id := range mapped {
			if !slices.Contains(out, id) {
				out = append(out, id)
			}
		}
	}
	return out
}
func legalDate(value any) any {
	if v, ok := value.(string); ok && len(v) >= 10 {
		if parsed, err := time.Parse("2006-01-02", v[:10]); err == nil {
			return parsed.Format("2006-01-02")
		}
	}
	return nil
}
func legalDefault(value M, key string, def any) any {
	if v, ok := value[key]; ok {
		return v
	}
	return def
}
func legalNames(value any) []M {
	out := []M{}
	if v, ok := value.(string); ok {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, M{"name": v})
		}
		return out
	}
	for _, v := range stringsOf(value) {
		if v != "" {
			out = append(out, M{"name": v})
		}
	}
	return out
}

// Public download links may point to courts' hosts. They are never fetched by
// the server, and cannot use an executable URL scheme or embedded credentials.
func legalPublicURL(raw string) any {
	if raw == "" {
		return nil
	}
	if strings.HasPrefix(raw, "/") && !strings.HasPrefix(raw, "//") {
		raw = "https://www.courtlistener.com" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil {
		return nil
	}
	return raw
}
func legalURLOrHome(raw string) string {
	if value, ok := legalPublicURL(raw).(string); ok {
		return value
	}
	return "https://www.courtlistener.com"
}

// Follow provider references by resource identity, never by requesting their
// supplied origin with the user's API token.
func legalResourceID(raw, resource string) string {
	u, err := url.Parse(raw)
	if err != nil || u.RawQuery != "" || u.Fragment != "" {
		return ""
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 2 || parts[len(parts)-2] != resource {
		return ""
	}
	id := parts[len(parts)-1]
	if resource == "courts" {
		if legalCourtIDRE.MatchString(id) {
			return id
		}
		return ""
	}
	if n, err := strconv.ParseInt(id, 10, 64); err == nil && n > 0 {
		return strconv.FormatInt(n, 10)
	}
	return ""
}
func (s *Server) legalCourtName(ctx context.Context, user, id string) any {
	if !legalCourtIDRE.MatchString(id) {
		return nil
	}
	cacheKey := "court-name:" + digest(s.cfg.CourtListenerURL+"|"+id)
	var cached string
	if s.db != nil && s.kvGet(ctx, cacheKey, &cached) == nil && cached != "" {
		return cached
	}
	data, err := s.courtJSON(ctx, user, "/courts/"+id+"/", nil)
	if err != nil {
		return nil
	}
	name := fallback(str(data, "full_name"), str(data, "short_name"))
	if name != "" && s.db != nil {
		_ = s.kvSet(ctx, cacheKey, name, 24*time.Hour)
	}
	return nullable(name)
}
