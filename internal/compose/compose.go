// Package compose writes a validated spec as a self-contained Terraform or
// Ansible project. The output is deterministic: the same spec always yields
// the same bytes, so a re-run of the same request produces no diff.
package compose

import (
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/axonops/axai/internal/catalogue"
	"github.com/axonops/axai/internal/spec"
)

// Files maps a project-relative path to its content.
type Files map[string]string

// Render returns the project files for s. s must have passed guard.Validate.
func Render(s spec.Spec) (Files, error) {
	switch s.Backend {
	case spec.Terraform:
		return renderTerraform(s)
	case spec.Ansible:
		return renderAnsible(s)
	}
	return nil, fmt.Errorf("unknown backend %q", s.Backend)
}

// Secrets returns the sorted secret names used anywhere in s.
func Secrets(s spec.Spec) []string {
	set := map[string]bool{}
	var walk func(any)
	walk = func(v any) {
		switch t := v.(type) {
		case string:
			if n, ok := strings.CutPrefix(t, spec.SecretPrefix); ok {
				set[n] = true
			}
		case []any:
			for _, e := range t {
				walk(e)
			}
		case map[string]any:
			for _, e := range t {
				walk(e)
			}
		}
	}
	for _, r := range s.Resources {
		walk(r.Fields)
	}
	out := make([]string, 0, len(set))
	for n := range set {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func kindOf(r spec.Resource) catalogue.Kind {
	k, _ := catalogue.Lookup(r.Kind)
	return k
}

// fieldsWithName returns the resource's fields with the identity field set.
func fieldsWithName(r spec.Resource, k catalogue.Kind) map[string]any {
	out := make(map[string]any, len(r.Fields)+1)
	for n, v := range r.Fields {
		out[n] = v
	}
	if k.Identity != "" {
		out[k.Identity] = r.Name
	}
	return out
}

func yamlDoc(v any) (string, error) {
	var b strings.Builder
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(v); err != nil {
		return "", err
	}
	return b.String(), enc.Close()
}
