// Package catalogue lists the AxonOps resource kinds axai can manage, with
// their fields and how each maps to the Terraform provider and the Ansible
// collection. The field lists come from the provider's docs/resources pages
// and the collection's module argument specs.
package catalogue

import (
	"fmt"
	"slices"
	"sort"
)

// Pinned releases written into every generated project.
const (
	ProviderSource    = "axonops/axonops"
	ProviderVersion   = "~> 0.1"
	CollectionName    = "axonops.axonops"
	CollectionVersion = ">=0.7.1"
	AnsibleRole       = "axonops.axonops.configurations"
	TerraformMinimum  = ">= 1.5.0"
)

// Type is a field's value type.
type Type string

// Field value types.
const (
	String     Type = "string"
	Number     Type = "number"
	Bool       Type = "bool"
	StringList Type = "list(string)"
	// Panels is a list of dashboard panels; guard and compose know its shape.
	Panels Type = "list(panel)"
)

// Field describes one attribute of a resource kind.
type Field struct {
	Name     string
	Type     Type
	Required bool
	Enum     []string // allowed values; for StringList, allowed elements
	Secret   bool     // value must be a $secret: reference
	// Backends limits the field to these backends; empty means all.
	Backends []string
}

// Kind is one manageable resource.
type Kind struct {
	Name          string            // neutral name used in a spec
	ClusterTypes  []string          // cluster types the kind applies to
	Terraform     string            // Terraform resource type
	AnsibleVar    string            // role variable holding a list of items, or "" if unsupported
	AnsibleRename map[string]string // neutral field -> Ansible item key, where they differ
	Fields        []Field
	// Identity is the field that names the object; deleting needs only it.
	Identity string
	// Notes is guidance shown to the model for this kind.
	Notes string
	// TerraformFields and AnsibleFields adapt a validated spec's fields to the
	// backend's attribute names; nil leaves them unchanged.
	TerraformFields func(map[string]any) map[string]any
	AnsibleFields   func(map[string]any) map[string]any
}

// Supports reports whether the kind can be generated for the backend.
func (k Kind) Supports(backend string) bool {
	switch backend {
	case "terraform":
		return k.Terraform != ""
	case "ansible":
		return k.AnsibleVar != ""
	}
	return false
}

// Field returns the named field.
func (k Kind) Field(name string) (Field, bool) {
	for _, f := range k.Fields {
		if f.Name == name {
			return f, true
		}
	}
	return Field{}, false
}

var kinds = map[string]Kind{}

func register(k Kind) { kinds[k.Name] = k }

// Lookup returns the named kind.
func Lookup(name string) (Kind, bool) {
	k, ok := kinds[name]
	return k, ok
}

// Names lists every kind in sorted order.
func Names() []string {
	out := make([]string, 0, len(kinds))
	for n := range kinds {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// ValidateKind checks that name exists and applies to clusterType.
func ValidateKind(name, clusterType string) (Kind, error) {
	k, ok := Lookup(name)
	if !ok {
		return Kind{}, fmt.Errorf("unknown resource kind %q (known: %v)", name, Names())
	}
	if !slices.Contains(k.ClusterTypes, clusterType) {
		return Kind{}, fmt.Errorf("resource kind %q does not apply to %s clusters", name, clusterType)
	}
	return k, nil
}

// FieldFor reports whether f applies to backend.
func (f Field) FieldFor(backend string) bool {
	return len(f.Backends) == 0 || slices.Contains(f.Backends, backend)
}
