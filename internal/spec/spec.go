// Package spec defines the infrastructure-as-code specification that the
// design stage produces, the guard stage validates and the compose stage
// renders. It imports no other axai package so the three stages stay
// independent.
package spec

// Backend names the tool that applies a generated project.
type Backend string

// Supported backends.
const (
	Terraform Backend = "terraform"
	Ansible   Backend = "ansible"
)

// Valid reports whether b is a supported backend.
func (b Backend) Valid() bool { return b == Terraform || b == Ansible }

// Action is what a request does to its resources.
type Action string

// Supported actions.
const (
	Create Action = "create"
	Update Action = "update"
	Delete Action = "delete"
)

// SecretPrefix marks a field value that refers to a secret held in the
// environment instead of containing it. "$secret:SLACK_WEBHOOK" becomes a
// sensitive Terraform variable or an environment lookup in Ansible.
const SecretPrefix = "$secret:"

// Spec is a complete, backend-neutral description of the resources to manage.
type Spec struct {
	Backend     Backend    `json:"backend"`
	Org         string     `json:"org"`
	Cluster     string     `json:"cluster"`
	ClusterType string     `json:"cluster_type"`
	Resources   []Resource `json:"resources"`
	// Assumptions lists every gap the model filled, shown to the user at the
	// confirmation step.
	Assumptions []string `json:"assumptions"`
}

// Resource is one managed object. Fields use the catalogue's neutral names.
type Resource struct {
	Kind   string         `json:"kind"`
	Name   string         `json:"name"`
	Action Action         `json:"action"`
	Fields map[string]any `json:"fields"`
}

// Destructive reports whether the spec removes anything.
func (s Spec) Destructive() bool {
	for _, r := range s.Resources {
		if r.Action == Delete {
			return true
		}
	}
	return false
}
