package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
)

// schemaErrPrinter is the message.Printer that jsonschema's
// ErrorKind.LocalizedString needs to format some error kinds
// (Pattern is one — it requires a non-nil printer to produce
// "does not match pattern X"). nil panics; English is fine.
var schemaErrPrinter = message.NewPrinter(language.English)

// v0.3 manifests are validated structurally against the canonical
// JSON Schema in schema/manifest.v0.3.schema.json (single source of
// truth — LLMs and external tools look there too). Cross-field
// rules from schema/AGENTS.md that JSON Schema can't express are
// applied separately by validateV3CrossField.
//
// The validator deliberately does NOT use Go structs for v0.3; the
// schema evolves field-by-field and re-deriving structs on every
// bump is friction without payoff. map[string]any preserves anything
// new without code changes.

const schemaV3Path = "schema/manifest.v0.3.schema.json"

// v3SchemaVersionRE matches the schema_version field in v0.3
// manifests, including future patch versions (0.3.1, 0.3.2, ...).
// Aligns with the pattern in manifest.v0.3.schema.json.
var v3SchemaVersionRE = regexp.MustCompile(`^0\.3(\.[0-9]+)?$`)

// peekSchemaVersion reads just the schema_version field from a
// manifest path without parsing the whole document. Cheap dispatcher
// for loadManifest — older v0.1/v0.2 manifests continue down the
// existing Manifest-struct path; v0.3 takes the JSON Schema route.
func peekSchemaVersion(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var shape struct {
		SchemaVersion string `json:"schema_version"`
	}
	if err := json.Unmarshal(b, &shape); err != nil {
		return "", err
	}
	return shape.SchemaVersion, nil
}

// isV3 reports whether v matches 0.3 or 0.3.<patch>.
func isV3(v string) bool {
	return v3SchemaVersionRE.MatchString(v)
}

// v3Compiled holds the compiled v0.3 schema. Lazily built on first
// use because compilation reads from disk and we want a clear error
// if the schema file is missing (e.g. running validator outside the
// repo root) rather than init-time panic.
var v3Compiled *jsonschema.Schema

// loadV3Schema compiles the canonical schema file. Cached on success.
// Called by validateV3 — callers don't need to invoke directly.
func loadV3Schema(root string) (*jsonschema.Schema, error) {
	if v3Compiled != nil {
		return v3Compiled, nil
	}
	schemaPath := filepath.Join(root, schemaV3Path)
	b, err := os.ReadFile(schemaPath)
	if err != nil {
		return nil, fmt.Errorf("read schema %s: %w (run validator from grove-apps repo root)", schemaPath, err)
	}
	var schemaDoc any
	if err := json.Unmarshal(b, &schemaDoc); err != nil {
		return nil, fmt.Errorf("parse schema %s: %w", schemaPath, err)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource(schemaPath, schemaDoc); err != nil {
		return nil, fmt.Errorf("add schema resource: %w", err)
	}
	s, err := c.Compile(schemaPath)
	if err != nil {
		return nil, fmt.Errorf("compile schema: %w", err)
	}
	v3Compiled = s
	return s, nil
}

// validateV3 runs structural (JSON Schema) + cross-field validation
// on a v0.3 manifest. Returns the parsed document on success so
// downstream Layer 2 can extract images / arch without re-parsing.
func validateV3(path, root string) (map[string]any, []validationError) {
	var errs []validationError
	add := func(field, msg string) {
		errs = append(errs, validationError{File: path, Field: field, Msg: msg})
	}

	b, err := os.ReadFile(path)
	if err != nil {
		add("", "read: "+err.Error())
		return nil, errs
	}
	var doc any
	if err := json.Unmarshal(b, &doc); err != nil {
		add("", "json: "+err.Error())
		return nil, errs
	}

	schema, err := loadV3Schema(root)
	if err != nil {
		add("", "schema load: "+err.Error())
		return nil, errs
	}

	if err := schema.Validate(doc); err != nil {
		// jsonschema's error tree has one entry per failed leaf
		// constraint. Surface each as its own validationError so CI
		// logs point at the exact field, not a single 200-line dump.
		var verr *jsonschema.ValidationError
		if !errorAs(err, &verr) {
			add("", "schema validate: "+err.Error())
			return doc.(map[string]any), errs
		}
		for _, leaf := range flattenJSONSchemaErrors(verr) {
			add(leaf.field, leaf.msg)
		}
	}

	// Cross-field rules from schema/AGENTS.md.
	m, _ := doc.(map[string]any)
	if m != nil {
		errs = append(errs, validateV3CrossField(path, m)...)

		// Slug↔path consistency (Rule 7-adjacent: filesystem layout
		// must match the slug field).
		rel, _ := filepath.Rel(filepath.Join(root, "apps"), filepath.Dir(path))
		if slug, _ := m["slug"].(string); slug != rel {
			add("slug", fmt.Sprintf("path-vs-slug mismatch: path implies %q, slug is %q", rel, slug))
		}
	}

	return m, errs
}

// errorAs avoids importing errors package just for one .As call;
// keeps the v3-specific file self-contained.
func errorAs(err error, target **jsonschema.ValidationError) bool {
	if v, ok := err.(*jsonschema.ValidationError); ok {
		*target = v
		return true
	}
	return false
}

// jsonSchemaErr is a flattened leaf-level schema validation error
// with a human-pointable field path and one-line message.
type jsonSchemaErr struct {
	field string
	msg   string
}

// flattenJSONSchemaErrors walks the jsonschema validation-error
// tree and returns one entry per leaf failure. Skips intermediate
// "subschema X failed" nodes that just point at children.
func flattenJSONSchemaErrors(root *jsonschema.ValidationError) []jsonSchemaErr {
	var out []jsonSchemaErr
	var walk func(e *jsonschema.ValidationError)
	walk = func(e *jsonschema.ValidationError) {
		if len(e.Causes) == 0 {
			field := strings.Join(stringifyJSONPointer(e.InstanceLocation), ".")
			if field == "" {
				field = "(root)"
			}
			out = append(out, jsonSchemaErr{field: field, msg: e.ErrorKind.LocalizedString(schemaErrPrinter)})
			return
		}
		for _, c := range e.Causes {
			walk(c)
		}
	}
	walk(root)
	return out
}

// stringifyJSONPointer converts the jsonschema library's InstanceLocation
// (a []string path) to a dotted/indexed display path:
//
//	["services", "0", "env"]        → "services[0].env"
//	["services", "1", "ports", "0"] → "services[1].ports[0]"
//
// Index detection: any segment that parses as int >=0 is rendered
// as [N], otherwise .name.
func stringifyJSONPointer(loc []string) []string {
	if len(loc) == 0 {
		return nil
	}
	out := []string{loc[0]}
	for _, seg := range loc[1:] {
		if isDigits(seg) {
			out[len(out)-1] = out[len(out)-1] + "[" + seg + "]"
		} else {
			out = append(out, seg)
		}
	}
	return out
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// shimV3ToManifest extracts the subset of v0.3 fields that Layer 2
// (image availability) needs into the legacy Manifest struct. v0.3
// has multiple service images; for now Layer 2 still checks only
// the source.image (the primary app image — same as v0.2). Per-service
// image checking is a follow-up that requires Layer 2 to take a list,
// not a single string.
//
// Other Manifest fields stay zero; downstream code that touches them
// either runs before this point (validation) or guards on v0.3 via
// the dispatch in main.
func shimV3ToManifest(m map[string]any) *Manifest {
	shim := &Manifest{}
	if v, _ := m["schema_version"].(string); v != "" {
		shim.SchemaVersion = v
	}
	if v, _ := m["slug"].(string); v != "" {
		shim.Slug = v
	}
	if src, _ := m["source"].(map[string]any); src != nil {
		if img, _ := src["image"].(string); img != "" {
			shim.Image = img
		}
	}
	if arches, _ := m["arch"].([]any); arches != nil {
		for _, a := range arches {
			if s, _ := a.(string); s != "" {
				shim.Arch = append(shim.Arch, s)
			}
		}
	}
	return shim
}

// validateV3CrossField is the stub the cross-field rules live in
// (rule-1 through rule-9 from schema/AGENTS.md). The structural
// JSON Schema validation already covers what it can; this function
// catches the rest. Lives in a separate file (v3_rules.go) so each
// rule can be unit-tested independently.
//
// Wired here; implementations land in v3_rules.go.
func validateV3CrossField(path string, m map[string]any) []validationError {
	var errs []validationError
	errs = append(errs, ruleAtLeastOnePublicPort(path, m)...)
	errs = append(errs, ruleReferenceValidation(path, m)...)
	errs = append(errs, ruleRoleBackupCoupling(path, m)...)
	errs = append(errs, ruleDependsOnDAG(path, m)...)
	errs = append(errs, ruleNameUniqueness(path, m)...)
	errs = append(errs, ruleGroveOldURLScope(path, m)...)
	errs = append(errs, ruleInterpolationScope(path, m)...)
	return errs
}
