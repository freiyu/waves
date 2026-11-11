package core

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// Proposal limits.
const (
	MaxProposalBytes  = 1 << 20
	maxPhases         = 20
	maxTasksPerPhase  = 100
	maxSubtasks       = 20
	maxValidators     = 20
	maxCommandBytes   = 4096
	maxValidatorSecs  = 3600
	proposalSchemaVer = 1
)

var keyRE = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// ProposalError is one validation problem; Path is a JSON pointer.
type ProposalError struct {
	Code    string `json:"code"`
	Path    string `json:"path"`
	Message string `json:"message"`
}

// Plan is a validated, normalized proposal.
type Plan struct {
	SchemaVersion int         `json:"schema_version"`
	Summary       string      `json:"summary"`
	Assumptions   []string    `json:"assumptions"`
	OpenQuestions []string    `json:"open_questions"`
	Risks         []PlanRisk  `json:"risks"`
	Phases        []PlanPhase `json:"phases"`
}

type PlanRisk struct {
	Description string `json:"description"`
	Severity    string `json:"severity"`
}

type PlanPhase struct {
	Key          string          `json:"key"`
	Title        string          `json:"title"`
	Objective    string          `json:"objective"`
	Deliverables []string        `json:"deliverables"`
	Validators   []PlanValidator `json:"validators"`
	Tasks        []PlanTask      `json:"tasks"`
}

type PlanValidator struct {
	Type     string   `json:"type"`
	Name     string   `json:"name"`
	Command  string   `json:"command,omitempty"`
	TimeoutS int      `json:"timeout_s,omitempty"`
	Items    []string `json:"items,omitempty"`
}

type PlanTask struct {
	Key              string        `json:"key"`
	Title            string        `json:"title"`
	Description      string        `json:"description"`
	Priority         string        `json:"priority"`
	DependsOn        []string      `json:"depends_on"`
	Acceptance       []string      `json:"acceptance_criteria"`
	WorkspaceMode    string        `json:"workspace_mode"`
	SuggestedHarness string        `json:"suggested_harness,omitempty"`
	Subtasks         []PlanSubtask `json:"subtasks"`
}

type PlanSubtask struct {
	Key         string `json:"key"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

// pv is a validation pass that collects every error.
type pv struct{ errs []ProposalError }

func (v *pv) add(code, path, format string, a ...any) {
	v.errs = append(v.errs, ProposalError{code, path, fmt.Sprintf(format, a...)})
}

func ptr(base string, k any) string {
	s := fmt.Sprint(k)
	s = strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "/", "~1")
	return base + "/" + s
}

// object checks that x is an object with only the allowed keys.
func (v *pv) object(x any, path string, allowed ...string) map[string]any {
	m, ok := x.(map[string]any)
	if !ok {
		v.add("schema", path, "must be an object")
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		found := false
		for _, a := range allowed {
			if a == k {
				found = true
			}
		}
		if !found {
			v.add("schema", ptr(path, k), "unknown field %q", k)
		}
	}
	return m
}

func (v *pv) str(m map[string]any, path, key string, required bool, lo, hi int) string {
	x, ok := m[key]
	if !ok || x == nil {
		if required {
			v.add("schema", ptr(path, key), "is required")
		}
		return ""
	}
	s, ok := x.(string)
	if !ok {
		v.add("schema", ptr(path, key), "must be a string")
		return ""
	}
	n := utf8.RuneCountInString(s)
	if (required && strings.TrimSpace(s) == "") || n < lo || (hi > 0 && n > hi) {
		v.add("schema", ptr(path, key), "must have %d to %d characters", max(lo, 1), hi)
	}
	return s
}

func (v *pv) enum(m map[string]any, path, key, def string, opts ...string) string {
	s := v.str(m, path, key, false, 0, 0)
	if s == "" {
		if _, present := m[key]; present {
			v.add("schema", ptr(path, key), "must be one of %s", strings.Join(opts, ", "))
		}
		return def
	}
	for _, o := range opts {
		if o == s {
			return s
		}
	}
	v.add("schema", ptr(path, key), "must be one of %s", strings.Join(opts, ", "))
	return def
}

func (v *pv) strs(m map[string]any, path, key string) []string {
	out := []string{}
	x, ok := m[key]
	if !ok || x == nil {
		return out
	}
	a, ok := x.([]any)
	if !ok {
		v.add("schema", ptr(path, key), "must be an array of strings")
		return out
	}
	for i, e := range a {
		s, ok := e.(string)
		if !ok {
			v.add("schema", ptr(ptr(path, key), i), "must be a string")
			continue
		}
		out = append(out, s)
	}
	return out
}

func (v *pv) arr(m map[string]any, path, key string, limit int, required bool) []any {
	x, ok := m[key]
	if !ok || x == nil {
		if required {
			v.add("schema", ptr(path, key), "is required")
		}
		return nil
	}
	a, ok := x.([]any)
	if !ok {
		v.add("schema", ptr(path, key), "must be an array")
		return nil
	}
	if len(a) > limit {
		v.add("limit_exceeded", ptr(path, key), "at most %d entries are allowed, got %d", limit, len(a))
		return a[:limit]
	}
	return a
}

func (v *pv) key(m map[string]any, path string, seen map[string]string) string {
	k := v.str(m, path, "key", true, 1, 64)
	if k == "" {
		return ""
	}
	if !keyRE.MatchString(k) {
		v.add("schema", ptr(path, "key"), "must match %s", keyRE.String())
	}
	if prev, dup := seen[k]; dup {
		v.add("duplicate_key", ptr(path, "key"), "key %q is already used at %s", k, prev)
	} else {
		seen[k] = ptr(path, "key")
	}
	return k
}

// ValidateProposal parses and validates a proposal document. It collects
// every error. On success it returns the normalized plan.
func ValidateProposal(raw []byte) (*Plan, []ProposalError) {
	v := &pv{}
	if len(raw) > MaxProposalBytes {
		v.add("limit_exceeded", "", "the document is larger than %d bytes", MaxProposalBytes)
		return nil, v.errs
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var doc any
	if err := dec.Decode(&doc); err != nil {
		v.add("invalid_json", "", "not parseable JSON: %s", jsonProblemText(err))
		return nil, v.errs
	}
	if dec.More() {
		v.add("invalid_json", "", "more than one JSON value")
		return nil, v.errs
	}
	root := v.object(doc, "", "schema_version", "summary", "assumptions", "open_questions", "risks", "phases")
	if root == nil {
		return nil, v.errs
	}
	if n, ok := root["schema_version"].(json.Number); !ok || n.String() != fmt.Sprint(proposalSchemaVer) {
		v.add("schema_version", "/schema_version", "must be %d", proposalSchemaVer)
	}
	p := &Plan{SchemaVersion: proposalSchemaVer}
	p.Summary = v.str(root, "", "summary", true, 1, 2000)
	p.Assumptions = v.strs(root, "", "assumptions")
	p.OpenQuestions = v.strs(root, "", "open_questions")
	p.Risks = []PlanRisk{}
	for i, x := range v.arr(root, "", "risks", 100, false) {
		path := ptr("/risks", i)
		m := v.object(x, path, "description", "severity")
		if m == nil {
			continue
		}
		p.Risks = append(p.Risks, PlanRisk{v.str(m, path, "description", true, 1, 2000), v.enum(m, path, "severity", "medium", "low", "medium", "high")})
	}
	seen := map[string]string{}
	taskPhase := map[string]int{}   // task key -> phase index
	taskPath := map[string]string{} // task key -> pointer
	phases := v.arr(root, "", "phases", maxPhases, true)
	if root["phases"] != nil && len(phases) == 0 {
		v.add("phase_incomplete", "/phases", "at least one phase is required")
	}
	for pi, x := range phases {
		path := ptr("/phases", pi)
		m := v.object(x, path, "key", "title", "objective", "deliverables", "validators", "tasks")
		if m == nil {
			continue
		}
		ph := PlanPhase{Key: v.key(m, path, seen), Title: v.str(m, path, "title", true, 1, 120), Deliverables: v.strs(m, path, "deliverables")}
		ph.Objective = v.str(m, path, "objective", false, 0, 2000)
		if strings.TrimSpace(ph.Objective) == "" {
			v.add("phase_incomplete", ptr(path, "objective"), "a phase needs an objective")
		}
		vals := v.arr(m, path, "validators", maxValidators, false)
		if len(vals) == 0 {
			v.add("phase_incomplete", ptr(path, "validators"), "a phase needs at least one validator")
		}
		for vi, y := range vals {
			vp := ptr(ptr(path, "validators"), vi)
			vm := v.object(y, vp, "type", "name", "command", "timeout_s", "items")
			if vm == nil {
				continue
			}
			val := PlanValidator{Type: v.enum(vm, vp, "type", "", "command", "checklist"), Name: v.str(vm, vp, "name", true, 1, 120)}
			if _, ok := vm["type"]; !ok {
				v.add("schema", ptr(vp, "type"), "is required")
			}
			switch val.Type {
			case "command":
				val.Command = v.str(vm, vp, "command", false, 0, 0)
				switch {
				case strings.TrimSpace(val.Command) == "":
					v.add("command_invalid", ptr(vp, "command"), "the command is empty")
				case strings.ContainsRune(val.Command, 0):
					v.add("command_invalid", ptr(vp, "command"), "the command contains a NUL byte")
				case len(val.Command) > maxCommandBytes:
					v.add("command_invalid", ptr(vp, "command"), "the command is longer than %d bytes", maxCommandBytes)
				}
				n, ok := vm["timeout_s"].(json.Number)
				t, err := n.Int64()
				if !ok || err != nil || t < 1 || t > maxValidatorSecs {
					v.add("schema", ptr(vp, "timeout_s"), "must be an integer from 1 to %d", maxValidatorSecs)
				}
				val.TimeoutS = int(t)
				if _, ok := vm["items"]; ok {
					v.add("schema", ptr(vp, "items"), "a command validator has no items")
				}
			case "checklist":
				val.Items = v.strs(vm, vp, "items")
				if len(val.Items) == 0 {
					v.add("schema", ptr(vp, "items"), "a checklist needs at least one item")
				}
				if _, ok := vm["command"]; ok {
					v.add("schema", ptr(vp, "command"), "a checklist has no command")
				}
			}
			ph.Validators = append(ph.Validators, val)
		}
		tasks := v.arr(m, path, "tasks", maxTasksPerPhase, false)
		if len(tasks) == 0 {
			v.add("phase_incomplete", ptr(path, "tasks"), "a phase needs at least one task")
		}
		for ti, y := range tasks {
			tp := ptr(ptr(path, "tasks"), ti)
			tm := v.object(y, tp, "key", "title", "description", "priority", "depends_on", "acceptance_criteria", "workspace_mode", "suggested_harness", "subtasks")
			if tm == nil {
				continue
			}
			t := PlanTask{Key: v.key(tm, tp, seen), Title: v.str(tm, tp, "title", true, 1, 160), Description: v.str(tm, tp, "description", false, 0, 20000),
				Priority: v.enum(tm, tp, "priority", "normal", "low", "normal", "high"), DependsOn: v.strs(tm, tp, "depends_on"),
				Acceptance: v.strs(tm, tp, "acceptance_criteria"), WorkspaceMode: v.enum(tm, tp, "workspace_mode", "code_change", "read_only", "code_change"),
				SuggestedHarness: v.str(tm, tp, "suggested_harness", false, 0, 64), Subtasks: []PlanSubtask{}}
			if t.WorkspaceMode == "code_change" && len(t.Acceptance) == 0 {
				v.add("missing_acceptance", ptr(tp, "acceptance_criteria"), "a code_change task needs acceptance criteria")
			}
			for si, z := range v.arr(tm, tp, "subtasks", maxSubtasks, false) {
				sp := ptr(ptr(tp, "subtasks"), si)
				sm := v.object(z, sp, "key", "title", "description")
				if sm == nil {
					continue
				}
				t.Subtasks = append(t.Subtasks, PlanSubtask{v.key(sm, sp, seen), v.str(sm, sp, "title", true, 1, 160), v.str(sm, sp, "description", false, 0, 20000)})
			}
			if t.Key != "" {
				taskPhase[t.Key], taskPath[t.Key] = pi, tp
			}
			ph.Tasks = append(ph.Tasks, t)
		}
		p.Phases = append(p.Phases, ph)
	}
	// Dependencies: known, not in a later phase, no cycles.
	graph := map[string][]string{}
	for pi, ph := range p.Phases {
		for _, t := range ph.Tasks {
			for di, d := range t.DependsOn {
				dp := ptr(ptr(taskPath[t.Key], "depends_on"), di)
				dpi, ok := taskPhase[d]
				switch {
				case !ok:
					v.add("unknown_dependency", dp, "no task has the key %q", d)
				case dpi > pi:
					v.add("dependency_order", dp, "task %q depends on %q in a later phase", t.Key, d)
				default:
					graph[t.Key] = append(graph[t.Key], d)
				}
			}
		}
	}
	if cyc := findCycle(graph); cyc != nil {
		v.add("dependency_cycle", taskPath[cyc[0]], "dependency cycle: %s", strings.Join(cyc, " -> "))
	}
	if len(v.errs) > 0 {
		return nil, v.errs
	}
	return p, nil
}

func findCycle(g map[string][]string) []string {
	const (
		white = iota
		grey
		black
	)
	color := map[string]int{}
	var stack []string
	var cyc []string
	var visit func(string) bool
	visit = func(n string) bool {
		color[n] = grey
		stack = append(stack, n)
		for _, m := range g[n] {
			if color[m] == grey {
				for i, s := range stack {
					if s == m {
						cyc = append(append([]string{}, stack[i:]...), m)
					}
				}
				return true
			}
			if color[m] == white && visit(m) {
				return true
			}
		}
		stack = stack[:len(stack)-1]
		color[n] = black
		return false
	}
	keys := make([]string, 0, len(g))
	for k := range g {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if color[k] == white && visit(k) {
			return cyc
		}
	}
	return nil
}

func jsonProblemText(err error) string {
	s := err.Error()
	if len(s) > 160 {
		s = s[:160]
	}
	return s
}
