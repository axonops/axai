// Package design asks the model for a spec. Its output is untrusted: the
// guard runs inside the structured-output check, so a rejected spec goes
// back to the model with the violations and nothing invalid is returned.
package design

import (
	"context"
	"fmt"
	"strings"

	"github.com/axonops/axai/internal/catalogue"
	"github.com/axonops/axai/internal/guard"
	"github.com/axonops/axai/internal/llm"
	"github.com/axonops/axai/internal/read"
	"github.com/axonops/axai/internal/spec"
	"github.com/axonops/axai/internal/structured"
)

// Input is everything the design stage needs.
type Input struct {
	Request     string
	Org         string
	Cluster     string
	ClusterType string
	Backend     spec.Backend
	Context     read.Context
	// Secrets names the secret references available for values the request
	// gave, such as webhook URLs. The model never sees the values.
	Secrets []string
	// Existing is what the project already holds. The model returns only what
	// the request adds or changes.
	Existing spec.Spec
}

// Schema is the JSON schema of the model's reply.
func Schema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"resources", "assumptions", "unsupported"},
		"properties": map[string]any{
			"resources": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"required":             []string{"kind", "name", "action", "fields"},
					"properties": map[string]any{
						"kind":   map[string]any{"type": "string", "enum": catalogue.Names()},
						"name":   map[string]any{"type": "string", "minLength": 1},
						"action": map[string]any{"type": "string", "enum": []string{"create", "update", "delete"}},
						"fields": map[string]any{"type": "object"},
					},
				},
			},
			"assumptions": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"unsupported": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		},
	}
}

type reply struct {
	Resources   []spec.Resource `json:"resources"`
	Assumptions []string        `json:"assumptions"`
	Unsupported []string        `json:"unsupported"`
}

// UnsupportedError means the request needs something the catalogue cannot
// generate. The spec is not returned, so a partial result is never applied.
type UnsupportedError struct{ Parts []string }

func (e *UnsupportedError) Error() string {
	return "the request needs things axai cannot generate yet: " + strings.Join(e.Parts, "; ")
}

// Generate returns a spec that has passed the guard.
func Generate(ctx context.Context, p llm.Provider, in Input) (spec.Spec, error) {
	return generate(ctx, p, "design", System(in), "Request: "+in.Request, in)
}

// Revise asks the model to correct a spec after a failed apply.
func Revise(ctx context.Context, p llm.Provider, in Input, current spec.Spec, failure string) (spec.Spec, error) {
	prompt := fmt.Sprintf("Request: %s\n\nCurrent spec (JSON): %s\n\nThe %s run failed with this output:\n%s\n\nReturn the full corrected spec with the smallest change that fixes the failure.",
		in.Request, mustJSON(current), in.Backend, failure)
	return generate(ctx, p, "fixer", System(in), prompt, in)
}

func generate(ctx context.Context, p llm.Provider, stage, system, prompt string, in Input) (spec.Spec, error) {
	build := func(r reply) spec.Spec {
		return spec.Spec{
			Backend: in.Backend, Org: in.Org, Cluster: in.Cluster, ClusterType: in.ClusterType,
			Resources: r.Resources, Assumptions: r.Assumptions,
		}
	}
	r, err := structured.Generate[reply](ctx, p, structured.Request{
		Stage: stage, System: system, Prompt: prompt, Schema: Schema(), SchemaName: "axai_spec",
		Check: func(v any) error { return guard.Validate(build(v.(reply))) },
	})
	if err != nil {
		return spec.Spec{}, err
	}
	if len(r.Unsupported) > 0 {
		return spec.Spec{}, &UnsupportedError{Parts: r.Unsupported}
	}
	return build(r), nil
}

// System builds the system prompt: the rules the guard enforces, the kinds
// and fields available, and what the cluster actually contains.
func System(in Input) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You write AxonOps configuration for the %s cluster %q in organisation %q, to be applied with %s.\n",
		in.ClusterType, in.Cluster, in.Org, in.Backend)
	b.WriteString(`Rules, all enforced by a validator that rejects anything else:
- Use only the resource kinds and fields listed below. Never invent a kind or a field.
- Never ask questions. Fill every gap with a sensible value and record it in "assumptions".
- Never write a credential, token, password or webhook URL. Where a secret is needed, write "$secret:NAME" with NAME an upper-case environment variable name.
- Numbers are JSON numbers. List fields are JSON arrays of strings.
- Use action "delete" only if the request asks to remove something.
- dashboard and chart must be taken exactly from the cluster's dashboards listed below.
- Cover every part of the request. If any part cannot be expressed with the kinds below, do not guess and do not drop it silently: list it in "unsupported" (an empty list means everything is covered).
`)
	if len(in.Secrets) > 0 {
		fmt.Fprintf(&b, "- The request contained values you must not see, replaced by secret references: %s. Use them as field values, for example \"$secret:%s\".\n", strings.Join(in.Secrets, ", "), in.Secrets[0])
	}
	b.WriteString("\nResource kinds:\n")
	for _, n := range catalogue.Names() {
		k, _ := catalogue.Lookup(n)
		if !k.Supports(string(in.Backend)) {
			continue
		}
		fmt.Fprintf(&b, "- %s (clusters: %s). The resource \"name\" is the %s.\n", k.Name, strings.Join(k.ClusterTypes, ", "), k.Identity)
		if k.Notes != "" {
			fmt.Fprintf(&b, "    Notes: %s\n", k.Notes)
		}
		for _, f := range k.Fields {
			if f.Name == k.Identity || !f.FieldFor(string(in.Backend)) {
				continue
			}
			req := "optional"
			if f.Required {
				req = "required"
			}
			if f.Secret {
				req += ", secret reference only"
			}
			fmt.Fprintf(&b, "    %s: %s, %s", f.Name, f.Type, req)
			if len(f.Enum) > 0 {
				fmt.Fprintf(&b, ", one of %s", strings.Join(f.Enum, " | "))
			}
			b.WriteString("\n")
		}
	}
	if len(in.Existing.Resources) > 0 {
		b.WriteString("\nThis project already contains these resources (kind, name, fields):\n")
		for _, r := range in.Existing.Resources {
			fmt.Fprintf(&b, "- %s %q %s\n", r.Kind, r.Name, mustJSON(r.Fields))
		}
		b.WriteString(`Return ONLY what the request adds or changes. Never repeat an existing resource unless the request asks to change or remove it.
- To add something new, use action "create" with a name that is not listed.
- To change an existing resource, use action "update" with its exact kind and name and only the fields that change.
- To remove one, use action "delete".
- Reuse existing names when something new refers to them (for example a route to an existing integration).
`)
	}
	if len(in.Context.Dashboards) > 0 {
		b.WriteString("\nDashboards and charts on this cluster:\n")
		for _, d := range in.Context.Dashboards {
			fmt.Fprintf(&b, "- %s: %s\n", d.Name, strings.Join(d.Charts, "; "))
		}
	}
	return b.String()
}
