package compose

import (
	"fmt"
	"strings"

	"github.com/axonops/axai/internal/catalogue"
	"github.com/axonops/axai/internal/spec"
)

func renderAnsible(s spec.Spec) (Files, error) {
	groups := map[string][]map[string]any{}
	var order []string
	for _, r := range s.Resources {
		k := kindOf(r)
		f := fieldsWithName(r, k)
		if k.AnsibleFields != nil {
			f = k.AnsibleFields(f)
		}
		f = ansibleSecrets(f).(map[string]any)
		f["present"] = r.Action != spec.Delete
		if _, ok := groups[k.AnsibleVar]; !ok {
			order = append(order, k.AnsibleVar)
		}
		groups[k.AnsibleVar] = append(groups[k.AnsibleVar], f)
	}

	vars := map[string]any{
		"org":          s.Org,
		"cluster":      s.Cluster,
		"cluster_type": s.ClusterType,
	}
	for _, g := range order {
		vars[g] = groups[g]
	}
	varsYAML, err := yamlDoc(sortedMap(vars))
	if err != nil {
		return nil, err
	}

	site := `---
- name: Apply AxonOps configuration
  hosts: localhost
  connection: local
  gather_facts: false
  roles:
    - role: ` + catalogue.AnsibleRole + `
`
	reqs, err := yamlDoc(map[string]any{
		"collections": []any{map[string]any{"name": catalogue.CollectionName, "version": catalogue.CollectionVersion}},
	})
	if err != nil {
		return nil, err
	}
	example := "# Credentials come from the environment; nothing secret is stored here.\n" +
		"# export AXONOPS_API_TOKEN=...   (self-hosted)  or  AXONOPS_TOKEN=...  (SaaS)\n" +
		"# export AXONOPS_URL=https://axonops.example.com   (self-hosted only)\n"
	for _, n := range Secrets(s) {
		example += "# export " + n + "=...\n"
	}
	return Files{
		"site.yml":                   site,
		"requirements.yml":           "---\n" + reqs,
		"group_vars/all/axonops.yml": "---\n" + varsYAML,
		"ENVIRONMENT.md":             strings.TrimSpace(example) + "\n",
		"ansible.cfg":                "[defaults]\nroles_path = ./roles\ncollections_path = ./collections\nretry_files_enabled = false\n",
	}, nil
}

// ansibleSecrets turns $secret: references into environment lookups.
func ansibleSecrets(v any) any {
	switch t := v.(type) {
	case string:
		if n, ok := strings.CutPrefix(t, spec.SecretPrefix); ok {
			return fmt.Sprintf("{{ lookup('env', '%s') }}", n)
		}
		return t
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = ansibleSecrets(e)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, e := range t {
			out[k] = ansibleSecrets(e)
		}
		return out
	}
	return v
}

// sortedMap is a no-op for yaml.v3, which already sorts map keys; it exists
// to make the intent explicit.
func sortedMap(m map[string]any) map[string]any { return m }
