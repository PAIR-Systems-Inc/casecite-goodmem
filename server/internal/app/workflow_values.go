package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var workflowTemplateRE = regexp.MustCompile(`\{\{\s*([A-Za-z][A-Za-z0-9_-]*)((?:\.[A-Za-z0-9_-]+)*)\s*\}\}`)

func workflowOutput(outputs M, name string) M {
	for key, value := range outputs {
		if strings.EqualFold(key, name) {
			return object(value)
		}
	}
	return nil
}

func workflowWalk(value any, path []string) (any, bool) {
	for _, part := range path {
		switch v := value.(type) {
		case map[string]any:
			var found bool
			value, found = v[part]
			if !found {
				return nil, false
			}
		case []any:
			index, err := strconv.Atoi(part)
			if err != nil || index < 0 || index >= len(v) || !regexp.MustCompile(`^[0-9]+$`).MatchString(part) {
				return nil, false
			}
			value = v[index]
		case []M:
			index, err := strconv.Atoi(part)
			if err != nil || index < 0 || index >= len(v) || strings.HasPrefix(part, "+") {
				return nil, false
			}
			value = v[index]
		case []string:
			index, err := strconv.Atoi(part)
			if err != nil || index < 0 || index >= len(v) || strings.HasPrefix(part, "+") {
				return nil, false
			}
			value = v[index]
		default:
			return nil, false
		}
	}
	return value, true
}

// Match the upstream prompt serialization (including spaces and Unicode).
// Map keys have a stable order, so prompts and fixture comparisons are repeatable.
func workflowJSON(value any) string {
	var raw bytes.Buffer
	encoder := json.NewEncoder(&raw)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(value)
	var out strings.Builder
	quoted, escaped := false, false
	for _, r := range strings.TrimSuffix(raw.String(), "\n") {
		out.WriteRune(r)
		if quoted {
			if escaped {
				escaped = false
			} else if r == '\\' {
				escaped = true
			} else if r == '"' {
				quoted = false
			}
		} else if r == '"' {
			quoted = true
		} else if r == ',' || r == ':' {
			out.WriteByte(' ')
		}
	}
	return out.String()
}

func workflowValueText(value any) string {
	text := ""
	switch v := value.(type) {
	case nil:
	case string:
		text = v
	case bool:
		if v {
			text = "yes"
		} else {
			text = "no"
		}
	default:
		text = workflowJSON(value)
	}
	return charSlice(text, 0, 12000)
}

func renderWorkflowTemplate(text string, outputs M) M {
	used, missing := []string{}, []string{}
	rendered := workflowTemplateRE.ReplaceAllStringFunc(text, func(match string) string {
		parts := workflowTemplateRE.FindStringSubmatch(match)
		ref := parts[1] + parts[2]
		output := workflowOutput(outputs, parts[1])
		path := []string{"summary"}
		if parts[2] != "" {
			path = strings.Split(strings.TrimPrefix(parts[2], "."), ".")
		}
		value, found := workflowWalk(output, path)
		if output == nil || !found {
			missing = append(missing, ref)
			return ""
		}
		used = append(used, ref)
		return workflowValueText(value)
	})
	return M{"text": rendered, "used": used, "missing": missing}
}

func workflowPresent(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case string:
		return strings.TrimSpace(x) != ""
	case []any:
		return len(x) > 0
	case []M:
		return len(x) > 0
	case []string:
		return len(x) > 0
	case map[string]any:
		return len(x) > 0
	default:
		return true
	}
}

func workflowNumber(v any) (float64, bool) {
	switch x := v.(type) {
	case bool:
		if x {
			return 1, true
		}
		return 0, true
	case float64:
		return x, !math.IsNaN(x) && !math.IsInf(x, 0)
	case int:
		return float64(x), true
	case string:
		for _, token := range strings.Fields(strings.NewReplacer(",", "", "$", " ").Replace(x)) {
			if n, err := strconv.ParseFloat(token, 64); err == nil && !math.IsNaN(n) && !math.IsInf(n, 0) {
				return n, true
			}
		}
	}
	return 0, false
}

func workflowCompareText(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return strings.ToLower(strings.TrimSpace(s))
	}
	return strings.ToLower(workflowJSON(v))
}

func evaluateWorkflowCondition(condition M, outputs M) M {
	step, op := str(condition, "step"), fallback(str(condition, "op"), "exists")
	path := []string{}
	for _, p := range strings.Split(str(condition, "path"), ".") {
		if p != "" {
			path = append(path, p)
		}
	}
	output := workflowOutput(outputs, step)
	actual, found := workflowWalk(output, path)
	found = output != nil && found
	expected, result := condition["value"], false
	a, an := workflowNumber(actual)
	e, en := workflowNumber(expected)
	switch op {
	case "exists":
		result = found && workflowPresent(actual)
	case "not_exists":
		result = !found || !workflowPresent(actual)
	case "equals", "not_equals":
		result = workflowCompareText(actual) == workflowCompareText(expected)
		switch actual.(type) {
		case float64, int, bool:
			if an && en {
				result = a == e
			}
		}
		if op == "not_equals" {
			result = !result
		}
		result = found && result
	case "contains":
		needle := workflowCompareText(expected)
		switch v := actual.(type) {
		case map[string]any:
			for k := range v {
				if workflowCompareText(k) == needle {
					result = true
				}
			}
		case []any:
			for _, item := range v {
				if strings.Contains(workflowCompareText(item), needle) {
					result = true
				}
			}
		case []string:
			for _, item := range v {
				if strings.Contains(workflowCompareText(item), needle) {
					result = true
				}
			}
		default:
			result = strings.Contains(workflowCompareText(actual), needle)
		}
		result = found && result
	case "gt", "lt", "gte", "lte":
		if found && an && en {
			switch op {
			case "gt":
				result = a > e
			case "lt":
				result = a < e
			case "gte":
				result = a >= e
			case "lte":
				result = a <= e
			}
		}
	case "count_gt", "count_eq":
		count := -1
		switch v := actual.(type) {
		case string:
			count = len([]rune(v))
		case []any:
			count = len(v)
		case []M:
			count = len(v)
		case []string:
			count = len(v)
		case map[string]any:
			count = len(v)
		}
		if found && count >= 0 && en {
			result = float64(count) == e
			if op == "count_gt" {
				result = float64(count) > e
			}
		}
	}
	var shown any
	if found {
		shown = charSlice(workflowJSON(actual), 0, 200)
	}
	return M{"result": result, "step": step, "path": strings.Join(path, "."), "op": op, "value": expected, "found": found, "actual": shown}
}

func workflowOutputText(output M, limit int) string {
	parts := []string{}
	if summary := strings.TrimSpace(str(output, "summary")); summary != "" {
		parts = append(parts, summary)
	}
	if workflowPresent(output["data"]) {
		var pretty bytes.Buffer
		_ = json.Indent(&pretty, []byte(workflowJSON(output["data"])), "", " ")
		parts = append(parts, pretty.String())
	}
	return charSlice(strings.Join(parts, "\n"), 0, max(0, limit))
}

// Unlike the original cap, include the truncation markers in the byte budget.
// Keep provenance intact; fail explicitly if even that metadata cannot fit.
func capWorkflowOutput(output M, limit int) (M, error) {
	if limit <= 0 || len(jsonBytes(output)) <= limit {
		return output, nil
	}
	capped := M{}
	_ = json.Unmarshal(jsonBytes(output), &capped)
	data := object(capped["data"])
	if data == nil {
		data = M{}
		capped["data"] = data
	}
	keys := []string{}
	for key := range data {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := len(jsonBytes(data[keys[i]])), len(jsonBytes(data[keys[j]]))
		if a == b {
			return keys[i] < keys[j]
		}
		return a > b
	})
	capped["truncated"] = true
	dropped := []string{}
	for _, key := range keys {
		if len(jsonBytes(capped)) <= limit {
			break
		}
		dropped = append(dropped, key)
		capped["truncated_fields"] = dropped
		value := data[key]
		delete(data, key)
		if len(jsonBytes(capped)) > limit {
			continue
		}
		switch v := value.(type) {
		case string:
			runes := []rune(v)
			low, high := 0, len(runes)
			for low < high {
				mid := (low + high + 1) / 2
				data[key] = string(runes[:mid]) + "…"
				if len(jsonBytes(capped)) <= limit {
					low = mid
				} else {
					high = mid - 1
				}
			}
			data[key] = string(runes[:low]) + "…"
		case []any:
			low, high := 0, len(v)
			for low < high {
				mid := (low + high + 1) / 2
				data[key] = v[:mid]
				if len(jsonBytes(capped)) <= limit {
					low = mid
				} else {
					high = mid - 1
				}
			}
			if low > 0 {
				data[key] = v[:low]
			} else {
				delete(data, key)
			}
		}
		if len(jsonBytes(capped)) > limit {
			delete(data, key)
		}
	}
	if len(jsonBytes(capped)) > limit {
		return nil, bad("The workflow output's provenance exceeds WORKFLOW_MAX_OUTPUT_BYTES; increase that limit or reduce the document scope.")
	}
	return capped, nil
}

func resolveWorkflowInputs(step, run, outputs M) ([]string, error) {
	input := object(step["inputs"])
	ids := stringsOf(run["input_document_ids"])
	switch str(input, "source") {
	case "documents":
		ids = stringsOf(input["document_ids"])
	case "step":
		prior := workflowOutput(outputs, str(input, "step"))
		if prior == nil {
			return nil, bad(fmt.Sprintf("Step '%s' has no output to read documents from", str(input, "step")))
		}
		ids = stringsOf(object(prior["produced"])["documents"])
		if len(ids) == 0 {
			ids = stringsOf(prior["document_ids"])
		}
	}
	return uniqueWorkflowStrings(ids), nil
}

func uniqueWorkflowStrings(ids []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, id := range ids {
		if !seen[id] {
			out = append(out, id)
			seen[id] = true
		}
	}
	return out
}
