package core

import (
	"strings"
	"testing"
)

const goodProposal = `{
  "schema_version": 1,
  "summary": "Add a health endpoint",
  "risks": [{"description": "none", "severity": "low"}],
  "phases": [
    {"key": "p1", "title": "Build", "objective": "Write the endpoint", "deliverables": ["handler"],
     "validators": [{"type": "command", "name": "tests", "command": "go test ./...", "timeout_s": 60},
                    {"type": "checklist", "name": "review", "items": ["read the diff"]}],
     "tasks": [{"key": "p1-t1", "title": "Handler", "acceptance_criteria": ["returns 200"]},
               {"key": "p1-t2", "title": "Docs", "workspace_mode": "read_only", "depends_on": ["p1-t1"],
                "subtasks": [{"key": "p1-t2-s1", "title": "README"}]}]},
    {"key": "p2", "title": "Ship", "objective": "Release", "validators": [{"type": "checklist", "name": "ok", "items": ["tag"]}],
     "tasks": [{"key": "p2-t1", "title": "Tag", "depends_on": ["p1-t2"], "acceptance_criteria": ["tag exists"]}]}
  ]
}`

func TestValidProposalIsNormalized(t *testing.T) {
	p, errs := ValidateProposal([]byte(goodProposal))
	if errs != nil {
		t.Fatalf("errors: %+v", errs)
	}
	if len(p.Phases) != 2 || p.Phases[0].Tasks[0].Priority != "normal" || p.Phases[0].Tasks[0].WorkspaceMode != "code_change" || p.Phases[0].Validators[0].TimeoutS != 60 {
		t.Fatalf("normalized: %+v", p)
	}
}

func TestProposalErrorsAreAllCollected(t *testing.T) {
	cases := map[string]struct{ doc, code, path string }{
		"json":              {`{"schema_version":`, "invalid_json", ""},
		"version":           {strings.Replace(goodProposal, `"schema_version": 1`, `"schema_version": 2`, 1), "schema_version", "/schema_version"},
		"unknown":           {strings.Replace(goodProposal, `"summary"`, `"extra": 1, "summary"`, 1), "schema", "/extra"},
		"dup":               {strings.Replace(goodProposal, `"key": "p2-t1"`, `"key": "p1-t1"`, 1), "duplicate_key", "/phases/1/tasks/0/key"},
		"unknown dep":       {strings.Replace(goodProposal, `"depends_on": ["p1-t1"]`, `"depends_on": ["nope"]`, 1), "unknown_dependency", "/phases/0/tasks/1/depends_on/0"},
		"order":             {strings.Replace(goodProposal, `"depends_on": ["p1-t1"]`, `"depends_on": ["p2-t1"]`, 1), "dependency_order", "/phases/0/tasks/1/depends_on/0"},
		"cycle":             {strings.Replace(goodProposal, `"title": "Handler",`, `"title": "Handler", "depends_on": ["p1-t2"],`, 1), "dependency_cycle", ""},
		"acceptance":        {strings.Replace(goodProposal, `, "acceptance_criteria": ["returns 200"]`, ``, 1), "missing_acceptance", "/phases/0/tasks/0/acceptance_criteria"},
		"command":           {strings.Replace(goodProposal, `"go test ./..."`, `"  "`, 1), "command_invalid", "/phases/0/validators/0/command"},
		"nul":               {strings.Replace(goodProposal, `"go test ./..."`, `"go test\u0000"`, 1), "command_invalid", "/phases/0/validators/0/command"},
		"timeout":           {strings.Replace(goodProposal, `"timeout_s": 60`, `"timeout_s": 99999`, 1), "schema", "/phases/0/validators/0/timeout_s"},
		"no tasks":          {strings.Replace(goodProposal, `"tasks": [{"key": "p2-t1", "title": "Tag", "depends_on": ["p1-t2"], "acceptance_criteria": ["tag exists"]}]`, `"tasks": []`, 1), "phase_incomplete", "/phases/1/tasks"},
		"no validator type": {strings.Replace(goodProposal, `{"type": "command", "name": "tests"`, `{"name": "tests"`, 1), "schema", "/phases/0/validators/0/type"},
		"bad key":           {strings.Replace(goodProposal, `"key": "p1",`, `"key": "P 1",`, 1), "schema", "/phases/0/key"},
	}
	for name, c := range cases {
		_, errs := ValidateProposal([]byte(c.doc))
		found := false
		for _, e := range errs {
			if e.Code == c.code && (c.path == "" || e.Path == c.path) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: want %s at %q, got %+v", name, c.code, c.path, errs)
		}
	}
	// Several problems at once are all reported.
	bad := strings.Replace(strings.Replace(goodProposal, `"go test ./..."`, `""`, 1), `"key": "p2-t1"`, `"key": "p1-t1"`, 1)
	if _, errs := ValidateProposal([]byte(bad)); len(errs) < 2 {
		t.Fatalf("want every error, got %+v", errs)
	}
	if _, errs := ValidateProposal([]byte(strings.Repeat(" ", MaxProposalBytes+1))); len(errs) != 1 || errs[0].Code != "limit_exceeded" {
		t.Fatalf("oversized: %+v", errs)
	}
}
