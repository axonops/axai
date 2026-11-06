// Package guard validates a model-written spec with deterministic rules. A
// spec that passes can be rendered; one that fails never reaches compose.
package guard

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"

	"github.com/axonops/axai/internal/catalogue"
	"github.com/axonops/axai/internal/spec"
)

// Violation is one rule a spec breaks.
type Violation struct {
	Resource string // "<kind>/<name>", or "" for the spec itself
	Msg      string
}

func (v Violation) String() string {
	if v.Resource == "" {
		return v.Msg
	}
	return v.Resource + ": " + v.Msg
}

// Error carries every violation found.
type Error struct{ Violations []Violation }

func (e *Error) Error() string {
	parts := make([]string, len(e.Violations))
	for i, v := range e.Violations {
		parts[i] = v.String()
	}
	return "spec rejected: " + strings.Join(parts, "; ")
}

var (
	secretName = regexp.MustCompile(`^[A-Z][A-Z0-9_]{2,63}$`)
	// inlineSecret matches text that looks like a credential or a webhook.
	inlineSecret = regexp.MustCompile(`(?i)(hooks\.slack\.com/services/|webhook\.office\.com|events\.pagerduty\.com|bearer\s+[a-z0-9._-]{16,}|\b[a-z0-9]{32,}\b|-----BEGIN [A-Z ]*PRIVATE KEY-----)`)
	durationRE   = regexp.MustCompile(`^[0-9]+(s|m|h|d)$`)
)

// Validate checks s and returns *Error listing every violation.
func Validate(s spec.Spec) error {
	var vs []Violation
	add := func(res, format string, a ...any) {
		vs = append(vs, Violation{Resource: res, Msg: fmt.Sprintf(format, a...)})
	}

	if !s.Backend.Valid() {
		add("", "backend %q is not terraform or ansible", s.Backend)
	}
	if s.Org == "" {
		add("", "org is empty")
	}
	if s.Cluster == "" {
		add("", "cluster is empty")
	}
	if !slices.Contains([]string{"cassandra", "dse", "kafka"}, s.ClusterType) {
		add("", "cluster_type %q is not cassandra, dse or kafka", s.ClusterType)
	}
	if len(s.Resources) == 0 {
		add("", "no resources")
	}

	seen := map[string]bool{}
	for _, r := range s.Resources {
		id := r.Kind + "/" + r.Name
		kind, err := catalogue.ValidateKind(r.Kind, s.ClusterType)
		if err != nil {
			add(id, "%v", err)
			continue
		}
		if !kind.Supports(string(s.Backend)) {
			add(id, "kind is not supported by the %s backend", s.Backend)
			continue
		}
		switch r.Action {
		case spec.Create, spec.Update, spec.Delete:
		default:
			add(id, "action %q is not create, update or delete", r.Action)
		}
		if r.Action == spec.Delete && s.Backend == spec.Terraform {
			add(id, "delete is only supported by the ansible backend; remove the resource from the Terraform project instead")
		}
		if r.Name == "" {
			add(id, "name is empty")
		}
		if seen[id] {
			add(id, "duplicate resource")
		}
		seen[id] = true
		checkFields(r, kind, string(s.Backend), add)
	}
	if len(vs) > 0 {
		return &Error{Violations: vs}
	}
	return nil
}

type adder func(res, format string, a ...any)

func checkFields(r spec.Resource, k catalogue.Kind, backend string, add adder) {
	id := r.Kind + "/" + r.Name
	for name := range r.Fields {
		f, ok := k.Field(name)
		if !ok {
			add(id, "unknown field %q", name)
			continue
		}
		if !f.FieldFor(backend) {
			add(id, "field %q is not supported by the %s backend", name, backend)
		}
	}
	for _, f := range k.Fields {
		v, present := r.Fields[f.Name]
		if f.Name == k.Identity && !present {
			v, present = r.Name, true
		}
		if !present {
			if f.Required && r.Action == spec.Create && f.FieldFor(backend) {
				add(id, "required field %q is missing", f.Name)
			}
			continue
		}
		if err := checkValue(f, v); err != nil {
			add(id, "field %q: %v", f.Name, err)
		}
	}
}

func checkValue(f catalogue.Field, v any) error {
	switch f.Type {
	case catalogue.String:
		s, ok := v.(string)
		if !ok {
			return fmt.Errorf("want string, got %T", v)
		}
		return checkString(f, s)
	case catalogue.Number:
		n, ok := v.(float64)
		if !ok {
			return fmt.Errorf("want number, got %T", v)
		}
		if math.IsNaN(n) || math.IsInf(n, 0) {
			return errors.New("not a finite number")
		}
	case catalogue.Bool:
		if _, ok := v.(bool); !ok {
			return fmt.Errorf("want bool, got %T", v)
		}
	case catalogue.Panels:
		return checkPanels(v)
	case catalogue.StringList:
		items, ok := v.([]any)
		if !ok {
			return fmt.Errorf("want list of strings, got %T", v)
		}
		for _, it := range items {
			s, ok := it.(string)
			if !ok {
				return fmt.Errorf("list element: want string, got %T", it)
			}
			if err := checkString(f, s); err != nil {
				return err
			}
		}
	}
	return nil
}

func checkString(f catalogue.Field, s string) error {
	if ref, ok := strings.CutPrefix(s, spec.SecretPrefix); ok {
		if !f.Secret {
			return errors.New("a secret reference is not allowed here")
		}
		if !secretName.MatchString(ref) {
			return fmt.Errorf("secret reference %q must be an upper-case environment variable name", ref)
		}
		return nil
	}
	if f.Secret {
		return fmt.Errorf("must be %sNAME, not an inline value", spec.SecretPrefix)
	}
	if inlineSecret.MatchString(s) {
		return errors.New("looks like an inline credential; use a $secret: reference")
	}
	if len(f.Enum) > 0 && !slices.Contains(f.Enum, s) {
		return fmt.Errorf("%q is not one of %v", s, f.Enum)
	}
	if f.Name == "duration" && !durationRE.MatchString(s) {
		return fmt.Errorf("%q is not a duration like 15m or 1h", s)
	}
	return nil
}

func checkPanels(v any) error {
	items, ok := v.([]any)
	if !ok || len(items) == 0 {
		return errors.New("want a non-empty list of panels")
	}
	titles := map[string]bool{}
	for i, it := range items {
		p, ok := it.(map[string]any)
		if !ok {
			return fmt.Errorf("panel %d: want object, got %T", i, it)
		}
		for k := range p {
			if !slices.Contains(catalogue.PanelKeys, k) {
				return fmt.Errorf("panel %d: unknown key %q (allowed: %s)", i, k, strings.Join(catalogue.PanelKeys, ", "))
			}
		}
		title, _ := p["title"].(string)
		if title == "" {
			return fmt.Errorf("panel %d: title is required", i)
		}
		if title == "__EMPTY_ROW__" {
			return fmt.Errorf("panel %d: title __EMPTY_ROW__ is reserved", i)
		}
		if titles[title] {
			return fmt.Errorf("panel %d: duplicate title %q", i, title)
		}
		titles[title] = true
		typ, _ := p["type"].(string)
		if !slices.Contains(catalogue.PanelTypes, typ) {
			return fmt.Errorf("panel %q: type %q is not one of %v", title, typ, catalogue.PanelTypes)
		}
		q, hasQ := p["query"].(string)
		switch {
		case typ == "row" && hasQ:
			return fmt.Errorf("panel %q: a row has no query", title)
		case typ != "row" && strings.TrimSpace(q) == "":
			return fmt.Errorf("panel %q: query is required", title)
		}
		for _, s := range []string{q, fmt.Sprint(p["legend"])} {
			if inlineSecret.MatchString(s) {
				return fmt.Errorf("panel %q: looks like an inline credential", title)
			}
		}
		for _, k := range []string{"w", "h"} {
			if n, present := p[k]; present {
				f, ok := n.(float64)
				limit := float64(catalogue.GridWidth)
				if k == "h" {
					limit = 40
				}
				if !ok || f < 1 || f > limit || f != float64(int(f)) {
					return fmt.Errorf("panel %q: %s must be a whole number from 1 to %d", title, k, int(limit))
				}
			}
		}
	}
	return nil
}
