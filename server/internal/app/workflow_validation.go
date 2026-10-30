package app

import (
	"fmt"
	"math"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
)

var workflowKinds = []string{"extract_fields", "analyze_contract", "compare_contracts", "draft_document", "deep_research", "citation_check", "approval"}
var workflowNameRE = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,59}$`)
var workflowPathRE = regexp.MustCompile(`^[A-Za-z0-9_-]+(\.[A-Za-z0-9_-]+)*$`)
var workflowOps = []string{"exists", "not_exists", "equals", "not_equals", "contains", "gt", "lt", "gte", "lte", "count_gt", "count_eq"}

type workflowValidator struct {
	index  int
	issues []M
}

func (v *workflowValidator) issue(field, message string) {
	v.issues = append(v.issues, M{"index": v.index, "field": field, "message": message})
}
func (v *workflowValidator) text(in, out M, key, prefix string, minLen, maxLen int, def any, required bool) {
	value, exists := in[key]
	if !exists {
		value = def
	}
	if value == nil && !required {
		out[key] = nil
		return
	}
	t, ok := value.(string)
	if !ok {
		v.issue(prefix+key, "Expected text")
		return
	}
	if len([]rune(t)) < minLen || len([]rune(t)) > maxLen {
		v.issue(prefix+key, fmt.Sprintf("Must contain %d to %d characters", minLen, maxLen))
		return
	}
	out[key] = t
}
func (v *workflowValidator) list(in, out M, key, prefix string, minLen, maxLen int, nullable bool) {
	value, exists := in[key]
	if (!exists || value == nil) && nullable {
		out[key] = nil
		return
	}
	if !exists {
		value = []string{}
	}
	items := []string{}
	switch raw := value.(type) {
	case []string:
		items = raw
	case []any:
		for i, x := range raw {
			s, ok := x.(string)
			if !ok {
				v.issue(fmt.Sprintf("%s%s.%d", prefix, key, i), "Expected text")
				return
			}
			items = append(items, s)
		}
	default:
		v.issue(prefix+key, "Expected a list")
		return
	}
	if len(items) < minLen || len(items) > maxLen {
		v.issue(prefix+key, fmt.Sprintf("Provide between %d and %d entries", minLen, maxLen))
		return
	}
	out[key] = items
}
func (v *workflowValidator) enum(in, out M, key, prefix, def string, allowed []string) {
	x, exists := in[key]
	if !exists {
		x = def
	}
	s, ok := x.(string)
	if !ok || !slices.Contains(allowed, s) {
		v.issue(prefix+key, "Choose one of: "+strings.Join(allowed, ", "))
		return
	}
	out[key] = s
}

func (v *workflowValidator) params(kind string, raw any) M {
	in := object(raw)
	out := M{}
	if raw != nil {
		if _, ok := raw.(map[string]any); !ok {
			v.issue("params", "Expected an object")
			return out
		}
	}
	text := func(key string, min, max int, def any, required bool) {
		v.text(in, out, key, "params.", min, max, def, required)
	}
	list := func(key string, min, max int, nullable bool) { v.list(in, out, key, "params.", min, max, nullable) }
	switch kind {
	case "extract_fields":
		fields, err := reviewFields(in["fields"])
		if err != nil {
			v.issue("params.fields", err.Error())
		} else {
			out["fields"] = fields
		}
		text("table_name", 0, 300, nil, false)
	case "analyze_contract":
		text("contract_type", 0, 80, "auto", true)
		text("jurisdiction", 0, 120, nil, false)
		text("representing", 0, 300, nil, false)
		text("instructions", 0, 4000, nil, false)
		v.enum(in, out, "posture", "params.", "balanced", []string{"strict", "balanced"})
	case "compare_contracts":
		v.enum(in, out, "baseline", "params.", "first_input", []string{"first_input", "document"})
		text("baseline_document_id", 0, 64, nil, false)
		text("focus", 0, 2000, nil, false)
		if str(out, "baseline_document_id") != "" {
			out["baseline"] = "document"
		} else if str(out, "baseline") == "document" {
			v.issue("params.baseline_document_id", "Pick the baseline document to compare against")
		}
	case "draft_document":
		text("instructions", 3, 8000, nil, true)
		list("include_outputs_from", 0, 12, false)
		list("reference_document_ids", 0, 4, false)
	case "deep_research":
		text("question", 3, 4000, nil, true)
		list("tools", 1, 2, true)
		for i, t := range stringsOf(out["tools"]) {
			if !slices.Contains([]string{"documents", "case_law"}, t) {
				v.issue(fmt.Sprintf("params.tools.%d", i), "Choose documents or case_law")
			}
		}
	case "citation_check":
		list("citations", 0, 50, false)
		list("from_steps", 0, 12, true)
	case "approval":
		text("title", 1, 200, "Approval", true)
		text("instructions", 0, 4000, nil, false)
		x, exists := in["approvers"]
		if !exists {
			x = "owner"
		}
		if group, ok := x.(string); ok {
			if group != "owner" && group != "any_member" {
				v.issue("params.approvers", "Approvers must be 'owner', 'any_member' or a list of user ids")
			} else {
				out["approvers"] = group
			}
		} else {
			temp := M{}
			v.list(M{"approvers": x}, temp, "approvers", "params.", 0, 1000, false)
			ids := []string{}
			for _, id := range stringsOf(temp["approvers"]) {
				if id = strings.TrimSpace(id); id != "" {
					ids = append(ids, id)
				}
			}
			if len(ids) < 1 || len(ids) > 20 {
				v.issue("params.approvers", "Name between 1 and 20 approvers")
			} else {
				out["approvers"] = uniqueWorkflowStrings(ids)
			}
		}
	}
	return out
}

func (v *workflowValidator) step(in M) M {
	out := M{"condition": nil}
	v.text(in, out, "name", "", 1, 60, nil, true)
	name := strings.TrimSpace(str(out, "name"))
	out["name"] = name
	if str(out, "name") != "" && !workflowNameRE.MatchString(name) {
		v.issue("name", "Step names start with a letter and use letters, digits, '_' or '-' only")
	}
	v.enum(in, out, "kind", "", "", workflowKinds)
	out["params"] = v.params(str(out, "kind"), in["params"])
	inputs := object(in["inputs"])
	normal := M{}
	if raw, exists := in["inputs"]; exists {
		if _, ok := raw.(map[string]any); !ok {
			v.issue("inputs", "Expected an object")
		}
	}
	v.enum(inputs, normal, "source", "inputs.", "run", []string{"run", "documents", "step"})
	v.list(inputs, normal, "document_ids", "inputs.", 0, 1000, false)
	v.text(inputs, normal, "step", "inputs.", 0, 60, nil, false)
	if str(normal, "source") == "documents" && len(stringsOf(normal["document_ids"])) == 0 {
		v.issue("inputs.document_ids", "Pick at least one input document")
	}
	if str(normal, "source") == "step" && strings.TrimSpace(str(normal, "step")) == "" {
		v.issue("inputs.step", "Pick the earlier step to read documents from")
	}
	if str(normal, "source") != "documents" {
		normal["document_ids"] = []string{}
	}
	if str(normal, "source") != "step" {
		normal["step"] = nil
	}
	out["inputs"] = normal
	if raw := in["condition"]; raw != nil {
		condition, ok := raw.(map[string]any)
		if !ok {
			v.issue("condition", "Expected an object")
			return out
		}
		c := M{"value": condition["value"]}
		v.text(condition, c, "step", "condition.", 1, 60, nil, true)
		v.text(condition, c, "path", "condition.", 0, 300, "", true)
		v.enum(condition, c, "op", "condition.", "", workflowOps)
		c["step"] = strings.TrimSpace(str(c, "step"))
		path := strings.Trim(strings.TrimSpace(str(c, "path")), ".")
		c["path"] = path
		if path != "" && !workflowPathRE.MatchString(path) {
			v.issue("condition.path", "Dotted keys and list indexes only, e.g. data.analyses.0.issues")
		}
		op := str(c, "op")
		switch c["value"].(type) {
		case nil, string, int, float64, bool:
		default:
			v.issue("condition.value", "Expected text, a number or a boolean")
		}
		if op == "exists" || op == "not_exists" {
			c["value"] = nil
		} else if !workflowPresent(c["value"]) {
			v.issue("condition.value", fmt.Sprintf("The '%s' condition needs a value to compare with", op))
		}
		if slices.Contains([]string{"gt", "lt", "gte", "lte", "count_gt", "count_eq"}, op) {
			raw := fmt.Sprint(c["value"])
			n, err := strconv.ParseFloat(raw, 64)
			if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
				v.issue("condition.value", fmt.Sprintf("The '%s' condition needs a finite number", op))
			}
		}
		out["condition"] = c
	}
	return out
}

func workflowReferences(step M) M {
	refs := M{}
	if name := str(object(step["inputs"]), "step"); name != "" {
		refs["inputs.step"] = []string{name}
	}
	if name := str(object(step["condition"]), "step"); name != "" {
		refs["condition.step"] = []string{name}
	}
	params := object(step["params"])
	for _, key := range []string{"include_outputs_from", "from_steps"} {
		if names := stringsOf(params[key]); len(names) > 0 {
			refs["params."+key] = uniqueWorkflowStrings(names)
		}
	}
	for _, key := range []string{"instructions", "question"} {
		names := []string{}
		for _, match := range workflowTemplateRE.FindAllStringSubmatch(str(params, key), -1) {
			names = append(names, match[1])
		}
		if len(names) > 0 {
			refs["params."+key] = uniqueWorkflowStrings(names)
		}
	}
	return refs
}

func validateWorkflowSteps(raw any, maxSteps int) ([]M, []M) {
	steps, issues := []M{}, []M{}
	var entries []any
	switch v := raw.(type) {
	case []any:
		entries = v
	case []M:
		for _, s := range v {
			entries = append(entries, s)
		}
	}
	if len(entries) == 0 {
		return steps, []M{{"index": -1, "field": "steps", "message": "Add at least one step"}}
	}
	if len(entries) > maxSteps {
		issues = append(issues, M{"index": -1, "field": "steps", "message": fmt.Sprintf("A workflow may have at most %d steps on this server", maxSteps)})
	}
	if len(entries) > 50 {
		return steps, append(issues, M{"index": -1, "field": "steps", "message": "A workflow may have at most 50 steps"})
	}
	seen := map[string]bool{}
	for index, entry := range entries {
		in, ok := entry.(map[string]any)
		if !ok {
			issues = append(issues, M{"index": index, "field": "step", "message": "Each step is an object"})
			continue
		}
		v := workflowValidator{index: index, issues: []M{}}
		step := v.step(in)
		name := str(step, "name")
		lower := strings.ToLower(name)
		if len(v.issues) == 0 {
			if seen[lower] {
				v.issue("name", fmt.Sprintf("Another step is already named '%s'", name))
			}
			refs := workflowReferences(step)
			keys := []string{}
			for k := range refs {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, field := range keys {
				for _, ref := range stringsOf(refs[field]) {
					if strings.EqualFold(ref, name) {
						v.issue(field, "A step cannot refer to itself")
					} else if !seen[strings.ToLower(ref)] {
						v.issue(field, fmt.Sprintf("'%s' is not an earlier step; only steps above this one can be used", ref))
					}
				}
			}
		}
		if lower != "" {
			seen[lower] = true
		}
		steps = append(steps, step)
		issues = append(issues, v.issues...)
	}
	return steps, issues
}

func workflowFixedDocuments(steps []M) []string {
	ids := []string{}
	for _, step := range steps {
		ids = append(ids, stringsOf(object(step["inputs"])["document_ids"])...)
		p := object(step["params"])
		if id := str(p, "baseline_document_id"); id != "" {
			ids = append(ids, id)
		}
		ids = append(ids, stringsOf(p["reference_document_ids"])...)
	}
	return uniqueWorkflowStrings(ids)
}
