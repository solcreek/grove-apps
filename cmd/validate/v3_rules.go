package main

import (
	"fmt"
	"regexp"
	"strings"
)

// Cross-field rules for v0.3 manifests — the things JSON Schema
// can't express. Numbered per schema/AGENTS.md; rule numbers appear
// in the validator error output ("rule-N: ...") so a contributor
// can search AGENTS.md by number to find the fix.
//
// Each rule is a separate function so it can be unit-tested in
// isolation; the dispatcher in v3.go (validateV3CrossField) calls
// them all.

// interpolationRE matches the three accepted ${X.NAME} forms.
// Captures: 1=namespace (secret|user|grove), 2=name.
var interpolationRE = regexp.MustCompile(`\$\{(secret|user|grove)\.([A-Za-z_][A-Za-z0-9_]*)\}`)

// escapedDollarRE matches the literal-${ escape we explicitly allow
// in env values. Stripped before checking interpolation references
// to avoid false-positive "reference to nonexistent secret" errors.
var escapedDollarRE = regexp.MustCompile(`\$\$\{`)

// ruleAtLeastOnePublicPort — rule-2.
// At least one service in services[] must declare one port with
// public: true. Without this the app boots but is unreachable.
func ruleAtLeastOnePublicPort(path string, m map[string]any) []validationError {
	services, _ := m["services"].([]any)
	for _, svcRaw := range services {
		svc, _ := svcRaw.(map[string]any)
		ports, _ := svc["ports"].([]any)
		for _, pRaw := range ports {
			p, _ := pRaw.(map[string]any)
			if pub, _ := p["public"].(bool); pub {
				return nil
			}
		}
	}
	return []validationError{{
		File:  path,
		Field: "services[*].ports[*].public",
		Msg:   "rule-2: at least one service must have a port with public: true (otherwise app is unreachable externally)",
	}}
}

// ruleReferenceValidation — rule-3.
// Every ${secret.NAME} must point to a declared secret; every
// ${user.NAME} must point to a declared user_input. Same for typed
// fields like backup.pg_dump.password_secret.
func ruleReferenceValidation(path string, m map[string]any) []validationError {
	declaredSecrets := collectNames(m, "secrets")
	declaredUsers := collectNames(m, "user_inputs")

	var errs []validationError
	add := func(field, msg string) {
		errs = append(errs, validationError{File: path, Field: field, Msg: msg})
	}

	services, _ := m["services"].([]any)
	for i, svcRaw := range services {
		svc, _ := svcRaw.(map[string]any)
		svcName, _ := svc["name"].(string)
		prefix := fmt.Sprintf("services[%d]", i)

		// env values
		if env, _ := svc["env"].(map[string]any); env != nil {
			for k, v := range env {
				s, _ := v.(string)
				for _, ref := range extractRefs(s) {
					if e := checkRef(ref, declaredSecrets, declaredUsers); e != "" {
						add(fmt.Sprintf("%s.env.%s", prefix, k), e)
					}
				}
			}
		}

		// hook cmd args
		if hooks, _ := svc["hooks"].(map[string]any); hooks != nil {
			for hookKey, actions := range hooks {
				acts, _ := actions.([]any)
				for j, actRaw := range acts {
					act, _ := actRaw.(map[string]any)
					cmd, _ := act["cmd"].([]any)
					for _, argRaw := range cmd {
						arg, _ := argRaw.(string)
						for _, ref := range extractRefs(arg) {
							// post_restore is the only hook where
							// ${grove.old_public_url} is meaningful;
							// rule-8 below catches misuse separately.
							if e := checkRef(ref, declaredSecrets, declaredUsers); e != "" {
								add(fmt.Sprintf("%s.hooks.%s[%d].cmd", prefix, hookKey, j), e)
							}
						}
					}
				}
			}
		}

		// backup.pg_dump.password_secret (typed field, same lookup)
		volumes, _ := svc["volumes"].([]any)
		for vi, volRaw := range volumes {
			vol, _ := volRaw.(map[string]any)
			backup, _ := vol["backup"].(map[string]any)
			if backup == nil {
				continue
			}
			if ps, _ := backup["password_secret"].(string); ps != "" {
				if !declaredSecrets[ps] {
					add(fmt.Sprintf("%s.volumes[%d].backup.password_secret", prefix, vi),
						fmt.Sprintf("rule-3: references secret %q, not declared in top-level secrets[]", ps))
				}
			}
			// backup.exec snapshot / restore string interpolation
			for _, key := range []string{"snapshot", "restore"} {
				args, _ := backup[key].([]any)
				for _, argRaw := range args {
					arg, _ := argRaw.(string)
					for _, ref := range extractRefs(arg) {
						if e := checkRef(ref, declaredSecrets, declaredUsers); e != "" {
							add(fmt.Sprintf("%s.volumes[%d].backup.%s", prefix, vi, key), e)
						}
					}
				}
			}
		}

		_ = svcName // reserved for future per-service context in messages
	}
	return errs
}

// extractRefs returns every ${ns.name} reference in s (after first
// collapsing the literal-${ escape sequence so $${secret.FOO} is
// not treated as a reference).
type interpRef struct {
	namespace string
	name      string
}

func extractRefs(s string) []interpRef {
	// Strip $$ escapes by mangling them — replace $${ with a token
	// that interpolationRE won't match, then run the regex.
	stripped := escapedDollarRE.ReplaceAllString(s, "\x00\x00")
	matches := interpolationRE.FindAllStringSubmatch(stripped, -1)
	out := make([]interpRef, 0, len(matches))
	for _, m := range matches {
		out = append(out, interpRef{namespace: m[1], name: m[2]})
	}
	return out
}

func checkRef(r interpRef, secrets, users map[string]bool) string {
	switch r.namespace {
	case "secret":
		if !secrets[r.name] {
			return fmt.Sprintf("rule-3: references ${secret.%s}, not declared in top-level secrets[]", r.name)
		}
	case "user":
		if !users[r.name] {
			return fmt.Sprintf("rule-3: references ${user.%s}, not declared in top-level user_inputs[]", r.name)
		}
	case "grove":
		switch r.name {
		case "public_url", "old_public_url":
			// known names — old_public_url scope checked by rule-8
		default:
			return fmt.Sprintf("rule-3: references ${grove.%s}, not a known grove variable (use public_url or old_public_url)", r.name)
		}
	}
	return ""
}

// collectNames returns the set of name fields in a top-level array.
// Used for secrets[] and user_inputs[] lookup tables.
func collectNames(m map[string]any, key string) map[string]bool {
	out := map[string]bool{}
	arr, _ := m[key].([]any)
	for _, item := range arr {
		obj, _ := item.(map[string]any)
		if n, _ := obj["name"].(string); n != "" {
			out[n] = true
		}
	}
	return out
}

// ruleRoleBackupCoupling — rule-4.
// role=state REQUIRES backup; role=cache|ephemeral FORBIDS backup.
func ruleRoleBackupCoupling(path string, m map[string]any) []validationError {
	var errs []validationError
	add := func(field, msg string) {
		errs = append(errs, validationError{File: path, Field: field, Msg: msg})
	}

	services, _ := m["services"].([]any)
	for i, svcRaw := range services {
		svc, _ := svcRaw.(map[string]any)
		volumes, _ := svc["volumes"].([]any)
		for vi, volRaw := range volumes {
			vol, _ := volRaw.(map[string]any)
			role, _ := vol["role"].(string)
			if role == "" {
				role = "state" // matches schema default
			}
			_, hasBackup := vol["backup"]
			switch role {
			case "state":
				if !hasBackup {
					add(fmt.Sprintf("services[%d].volumes[%d]", i, vi),
						"rule-4: role=state requires backup block (role of \"state\" means this data is authoritative + included in deploy snapshot)")
				}
			case "cache", "ephemeral":
				if hasBackup {
					add(fmt.Sprintf("services[%d].volumes[%d].backup", i, vi),
						fmt.Sprintf("rule-4: role=%s forbids backup (only role=state volumes are snapshotted)", role))
				}
			}
		}
	}
	return errs
}

// ruleDependsOnDAG — rule-6.
// services[].depends_on must form a DAG (no cycles).
func ruleDependsOnDAG(path string, m map[string]any) []validationError {
	services, _ := m["services"].([]any)

	// Build adjacency map: serviceName → []dependencies
	deps := map[string][]string{}
	names := []string{}
	for _, svcRaw := range services {
		svc, _ := svcRaw.(map[string]any)
		n, _ := svc["name"].(string)
		if n == "" {
			continue
		}
		names = append(names, n)
		var ds []string
		if arr, _ := svc["depends_on"].([]any); arr != nil {
			for _, d := range arr {
				if s, _ := d.(string); s != "" {
					ds = append(ds, s)
				}
			}
		}
		deps[n] = ds
	}

	// DFS cycle detection. white = unvisited, gray = on current stack,
	// black = fully explored. A back-edge to gray = cycle.
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := map[string]int{}
	for _, n := range names {
		color[n] = white
	}

	var cycleAt string
	var dfs func(n string, stack []string) bool
	dfs = func(n string, stack []string) bool {
		color[n] = gray
		stack = append(stack, n)
		for _, d := range deps[n] {
			switch color[d] {
			case gray:
				// Found a back-edge — render the cycle for the error.
				idx := indexOf(stack, d)
				cycle := append(stack[idx:], d)
				cycleAt = strings.Join(cycle, " -> ")
				return true
			case white:
				if dfs(d, stack) {
					return true
				}
			}
		}
		color[n] = black
		return false
	}
	for _, n := range names {
		if color[n] == white {
			if dfs(n, nil) {
				return []validationError{{
					File:  path,
					Field: "services[*].depends_on",
					Msg:   "rule-6: depends_on cycle: " + cycleAt,
				}}
			}
		}
	}
	return nil
}

func indexOf(ss []string, s string) int {
	for i, v := range ss {
		if v == s {
			return i
		}
	}
	return -1
}

// ruleNameUniqueness — rule-7.
// services[].name unique within manifest; volumes[].name unique
// within each service; secrets[].name unique; user_inputs[].name
// unique; ports[].name unique within each service.
func ruleNameUniqueness(path string, m map[string]any) []validationError {
	var errs []validationError
	add := func(field, msg string) {
		errs = append(errs, validationError{File: path, Field: field, Msg: msg})
	}

	// Top-level arrays
	checkDup := func(arr []any, field string) {
		seen := map[string]int{}
		for i, item := range arr {
			obj, _ := item.(map[string]any)
			n, _ := obj["name"].(string)
			if n == "" {
				continue
			}
			if first, ok := seen[n]; ok {
				add(fmt.Sprintf("%s[%d].name", field, i),
					fmt.Sprintf("rule-7: duplicate name %q (first at index %d)", n, first))
			} else {
				seen[n] = i
			}
		}
	}

	if svcs, _ := m["services"].([]any); svcs != nil {
		checkDup(svcs, "services")
	}
	if secs, _ := m["secrets"].([]any); secs != nil {
		checkDup(secs, "secrets")
	}
	if ins, _ := m["user_inputs"].([]any); ins != nil {
		checkDup(ins, "user_inputs")
	}

	// Per-service: ports + volumes
	services, _ := m["services"].([]any)
	for i, svcRaw := range services {
		svc, _ := svcRaw.(map[string]any)
		if ports, _ := svc["ports"].([]any); ports != nil {
			checkDup(ports, fmt.Sprintf("services[%d].ports", i))
		}
		if vols, _ := svc["volumes"].([]any); vols != nil {
			checkDup(vols, fmt.Sprintf("services[%d].volumes", i))
		}
	}

	return errs
}

// ruleGroveOldURLScope — rule-8.
// ${grove.old_public_url} is valid ONLY in hooks.post_restore.cmd.
// Anywhere else (env, other hook types, backup) is a misuse.
func ruleGroveOldURLScope(path string, m map[string]any) []validationError {
	var errs []validationError
	add := func(field, msg string) {
		errs = append(errs, validationError{File: path, Field: field, Msg: msg})
	}

	hasOldURL := func(s string) bool {
		stripped := escapedDollarRE.ReplaceAllString(s, "\x00\x00")
		return strings.Contains(stripped, "${grove.old_public_url}")
	}

	services, _ := m["services"].([]any)
	for i, svcRaw := range services {
		svc, _ := svcRaw.(map[string]any)
		prefix := fmt.Sprintf("services[%d]", i)

		// env: any old_public_url is wrong
		if env, _ := svc["env"].(map[string]any); env != nil {
			for k, v := range env {
				if s, _ := v.(string); hasOldURL(s) {
					add(fmt.Sprintf("%s.env.%s", prefix, k),
						"rule-8: ${grove.old_public_url} is only valid in hooks.post_restore.cmd")
				}
			}
		}

		// hooks: only post_restore.cmd is allowed
		if hooks, _ := svc["hooks"].(map[string]any); hooks != nil {
			for hookKey, actions := range hooks {
				acts, _ := actions.([]any)
				for j, actRaw := range acts {
					act, _ := actRaw.(map[string]any)
					cmd, _ := act["cmd"].([]any)
					for _, argRaw := range cmd {
						arg, _ := argRaw.(string)
						if hasOldURL(arg) && hookKey != "post_restore" {
							add(fmt.Sprintf("%s.hooks.%s[%d].cmd", prefix, hookKey, j),
								fmt.Sprintf("rule-8: ${grove.old_public_url} is only valid in hooks.post_restore.cmd, not %s", hookKey))
						}
					}
				}
			}
		}
	}
	return errs
}

// ruleInterpolationScope — rule-1.
// ${secret.X} / ${user.X} / ${grove.X} forms in args or command are
// silently NOT interpolated. Catch attempted use and reject loudly.
func ruleInterpolationScope(path string, m map[string]any) []validationError {
	var errs []validationError
	add := func(field, msg string) {
		errs = append(errs, validationError{File: path, Field: field, Msg: msg})
	}

	containsInterp := func(s string) bool {
		stripped := escapedDollarRE.ReplaceAllString(s, "\x00\x00")
		return interpolationRE.MatchString(stripped)
	}

	services, _ := m["services"].([]any)
	for i, svcRaw := range services {
		svc, _ := svcRaw.(map[string]any)
		prefix := fmt.Sprintf("services[%d]", i)

		for _, key := range []string{"args", "command"} {
			arr, _ := svc[key].([]any)
			for j, item := range arr {
				if s, _ := item.(string); containsInterp(s) {
					add(fmt.Sprintf("%s.%s[%d]", prefix, key, j),
						fmt.Sprintf("rule-1: ${...} interpolation is not resolved in %s — only in env values and hook cmd. Read the value from an env var instead.", key))
				}
			}
		}
	}
	return errs
}
