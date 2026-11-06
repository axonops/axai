package compose

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/axonops/axai/internal/catalogue"
	"github.com/axonops/axai/internal/spec"
)

var nonIdent = regexp.MustCompile(`[^a-z0-9]+`)

func tfLabel(name string) string {
	l := strings.Trim(nonIdent.ReplaceAllString(strings.ToLower(name), "_"), "_")
	if l == "" || (l[0] >= '0' && l[0] <= '9') {
		l = "r_" + l
	}
	return l
}

func renderTerraform(s spec.Spec) (Files, error) {
	secrets := Secrets(s)

	versions := fmt.Sprintf(`terraform {
  required_version = %q

  required_providers {
    axonops = {
      source  = %q
      version = %q
    }
  }
}
`, catalogue.TerraformMinimum, catalogue.ProviderSource, catalogue.ProviderVersion)

	var vars strings.Builder
	vars.WriteString(`variable "axonops_org_id" {
  description = "AxonOps organisation."
  type        = string
  default     = ` + strconv.Quote(s.Org) + `
}

variable "axonops_api_key" {
  description = "AxonOps API key. Set TF_VAR_axonops_api_key or AXONOPS_API_KEY."
  type        = string
  sensitive   = true
  default     = null
}

variable "axonops_host" {
  description = "AxonOps server host for self-hosted installs; leave null for SaaS."
  type        = string
  default     = null
}
`)
	for _, n := range secrets {
		fmt.Fprintf(&vars, "\nvariable %q {\n  description = \"Secret value for %s. Set TF_VAR_%s.\"\n  type        = string\n  sensitive   = true\n}\n", strings.ToLower(n), n, strings.ToLower(n))
	}

	var main strings.Builder
	main.WriteString(`provider "axonops" {
  org_id       = var.axonops_org_id
  api_key      = var.axonops_api_key
  axonops_host = var.axonops_host
}
`)
	for _, r := range s.Resources {
		k := kindOf(r)
		if r.Action == spec.Delete {
			return nil, fmt.Errorf("%s/%s: delete is not supported by the terraform backend", r.Kind, r.Name)
		}
		f := fieldsWithName(r, k)
		if k.TerraformFields != nil {
			f = k.TerraformFields(f)
		}
		if r.Kind == "alert_route" {
			if ref := integrationRef(s, f); ref != "" {
				f["integration_name"] = catalogue.RawHCL(ref)
			}
		}
		f["cluster_name"] = s.Cluster
		f["cluster_type"] = s.ClusterType
		fmt.Fprintf(&main, "\nresource %q %q {\n", k.Terraform, tfLabel(r.Name))
		writeHCLBody(&main, f, 1)
		main.WriteString("}\n")
	}

	example := "# Copy to terraform.tfvars. Never commit real values.\n# axonops_org_id  = \"" + s.Org + "\"\n# axonops_host    = \"axonops.example.com\"\n"
	for _, n := range secrets {
		example += "# " + strings.ToLower(n) + " = \"\"\n"
	}
	return Files{
		"versions.tf":              versions,
		"variables.tf":             vars.String(),
		"main.tf":                  main.String(),
		"terraform.tfvars.example": example,
	}, nil
}

func writeHCLBody(b *strings.Builder, f map[string]any, depth int) {
	pad := strings.Repeat("  ", depth)
	keys := sortedKeys(f)
	width := 0
	for _, k := range keys {
		_, nested := f[k].(map[string]any)
		if items, ok := f[k].([]any); ok && hasMaps(items) {
			nested = true
		}
		if !nested && len(k) > width {
			width = len(k)
		}
	}
	for _, k := range keys {
		if items, ok := f[k].([]any); ok && hasMaps(items) {
			fmt.Fprintf(b, "%s%s = [\n", pad, k)
			for _, it := range items {
				fmt.Fprintf(b, "%s  {\n", pad)
				writeHCLBody(b, it.(map[string]any), depth+2)
				fmt.Fprintf(b, "%s  },\n", pad)
			}
			fmt.Fprintf(b, "%s]\n", pad)
			continue
		}
		if m, ok := f[k].(map[string]any); ok {
			fmt.Fprintf(b, "%s%s = {\n", pad, k)
			writeHCLBody(b, m, depth+1)
			fmt.Fprintf(b, "%s}\n", pad)
			continue
		}
		fmt.Fprintf(b, "%s%-*s = %s\n", pad, width, k, hclValue(f[k]))
	}
}

func hasMaps(items []any) bool {
	for _, it := range items {
		if _, ok := it.(map[string]any); ok {
			return true
		}
	}
	return false
}

func hclValue(v any) string {
	switch t := v.(type) {
	case catalogue.RawHCL:
		return string(t)
	case string:
		if n, ok := strings.CutPrefix(t, spec.SecretPrefix); ok {
			return "var." + strings.ToLower(n)
		}
		return hclString(t)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	case []any:
		parts := make([]string, len(t))
		for i, e := range t {
			parts[i] = hclValue(e)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case []string:
		sort.Strings(t)
		parts := make([]string, len(t))
		for i, e := range t {
			parts[i] = hclString(e)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	}
	return hclString(fmt.Sprint(v))
}

// hclString quotes s and escapes "${" and "%{" so values are never
// interpolated.
func hclString(s string) string {
	q := strconv.Quote(s)
	q = strings.ReplaceAll(q, "${", "$${")
	return strings.ReplaceAll(q, "%{", "%%{")
}

// integrationRef returns a Terraform reference to the integration a route
// names, when the same spec creates it, so Terraform applies the integration
// first. It returns "" when the integration is not in the spec.
func integrationRef(s spec.Spec, route map[string]any) string {
	name, _ := route["integration_name"].(string)
	typ, _ := route["integration_type"].(string)
	for _, r := range s.Resources {
		if r.Kind == typ+"_integration" && r.Name == name && r.Action != spec.Delete {
			k := kindOf(r)
			return k.Terraform + "." + tfLabel(r.Name) + ".name"
		}
	}
	return ""
}
