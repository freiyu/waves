package contract

import (
	"bytes"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestRoutesUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, r := range Routes {
		k := r.Method + " " + r.Path
		if seen[k] {
			t.Errorf("duplicate route %s", k)
		}
		seen[k] = true
		if r.Scope != "none" && r.Scope != "op" && r.Scope != "human" {
			t.Errorf("%s: unknown scope %q", k, r.Scope)
		}
	}
}

// Every JSON field must be required:"true" or omitempty; otherwise the
// generated TypeScript silently marks it optional.
func TestFieldsAreRequiredOrOmitempty(t *testing.T) {
	seen := map[reflect.Type]bool{}
	var walk func(reflect.Type)
	walk = func(rt reflect.Type) {
		for rt.Kind() == reflect.Pointer || rt.Kind() == reflect.Slice || rt.Kind() == reflect.Map {
			rt = rt.Elem()
		}
		if rt.Kind() != reflect.Struct || seen[rt] || rt.PkgPath() != "waves/internal/api/contract" {
			return
		}
		seen[rt] = true
		for i := 0; i < rt.NumField(); i++ {
			f := rt.Field(i)
			if f.Anonymous {
				walk(f.Type)
				continue
			}
			j, ok := f.Tag.Lookup("json")
			if ok {
				if f.Tag.Get("required") != "true" && !strings.Contains(j, "omitempty") {
					t.Errorf("%s.%s: json field needs required:\"true\" or omitempty", rt.Name(), f.Name)
				}
			}
			walk(f.Type)
		}
	}
	for _, r := range Routes {
		if r.Req != nil {
			walk(reflect.TypeOf(r.Req))
		}
		if r.Resp != nil {
			walk(reflect.TypeOf(r.Resp))
		}
	}
	walk(reflect.TypeOf(ErrorBody{}))
}

// web/openapi.json is generated; a stale copy means the client types lag the contract.
func TestGeneratedSpecIsCurrent(t *testing.T) {
	want, err := OpenAPI()
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile("../../../web/openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("web/openapi.json is stale: run `make gen`")
	}
}

// A route whose request carries the elevation header is a human route, unless it
// is one of the documented exceptions where only some fields need the elevation
// (the service checks those fields, and rejects unknown ones).
func TestElevationHeaderMeansHumanScope(t *testing.T) {
	fieldLevel := map[string]bool{"PATCH /settings": true, "POST /auth/stepup/revoke": true}
	for _, r := range Routes {
		if r.Req == nil {
			continue
		}
		if _, ok := reflect.TypeOf(r.Req).FieldByName("Elevation"); ok && r.Scope != "human" && !fieldLevel[r.Method+" "+r.Path] {
			t.Errorf("%s %s takes an elevation but is scoped %q", r.Method, r.Path, r.Scope)
		}
	}
}
