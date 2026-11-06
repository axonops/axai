package spec

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// Op says what a merge did to one resource.
type Op string

// Merge operations.
const (
	Added     Op = "added"
	Updated   Op = "updated"
	Unchanged Op = "unchanged"
	Removed   Op = "removed"
)

// Change is the effect of a merge on one resource.
type Change struct {
	Kind string
	Name string
	Op   Op
}

func (c Change) String() string { return fmt.Sprintf("%s %s/%s", c.Op, c.Kind, c.Name) }

// ConflictError means the request would change or remove something already in
// the project without asking to. Nothing is overwritten.
type ConflictError struct{ Problems []string }

func (e *ConflictError) Error() string { return strings.Join(e.Problems, "; ") }

func key(r Resource) string { return r.Kind + "/" + r.Name }

// Merge adds delta to base. A resource already in base is never replaced by a
// create: identical settings are a no-op and different settings are a
// conflict. Only an update changes it (the fields given override, the rest are
// kept) and only a delete removes it. Resources delta does not mention stay.
// Base and delta must target the same org, cluster, cluster type and backend.
func Merge(base, delta Spec) (Spec, []Change, error) {
	if len(base.Resources) == 0 && base.Org == "" {
		out := delta
		changes := make([]Change, len(delta.Resources))
		var problems []string
		for i, r := range delta.Resources {
			if r.Action == Update {
				problems = append(problems, fmt.Sprintf("%s does not exist in the project, so there is nothing to update", key(r)))
			}
			changes[i] = Change{r.Kind, r.Name, opFor(r.Action)}
		}
		if len(problems) > 0 {
			return Spec{}, nil, &ConflictError{Problems: problems}
		}
		return out, changes, nil
	}
	if base.Org != delta.Org || base.Cluster != delta.Cluster || base.ClusterType != delta.ClusterType || base.Backend != delta.Backend {
		return Spec{}, nil, &ConflictError{Problems: []string{fmt.Sprintf(
			"the project targets %s %s/%s with %s, not %s %s/%s with %s; use another --project",
			base.ClusterType, base.Org, base.Cluster, base.Backend, delta.ClusterType, delta.Org, delta.Cluster, delta.Backend)}}
	}
	out := base
	out.Resources = append([]Resource(nil), base.Resources...)
	out.Assumptions = append(append([]string(nil), base.Assumptions...), delta.Assumptions...)
	index := map[string]int{}
	for i, r := range out.Resources {
		index[key(r)] = i
	}
	var changes []Change
	var problems []string
	for _, d := range delta.Resources {
		i, exists := index[key(d)]
		switch d.Action {
		case Create:
			switch {
			case !exists:
				index[key(d)] = len(out.Resources)
				out.Resources = append(out.Resources, d)
				changes = append(changes, Change{d.Kind, d.Name, Added})
			case equalFields(out.Resources[i].Fields, d.Fields) && out.Resources[i].Action != Delete:
				changes = append(changes, Change{d.Kind, d.Name, Unchanged})
			default:
				problems = append(problems, fmt.Sprintf("%s already exists with different settings; ask to update it to change it", key(d)))
			}
		case Update:
			if !exists {
				problems = append(problems, fmt.Sprintf("%s does not exist in the project, so there is nothing to update", key(d)))
				continue
			}
			merged := map[string]any{}
			for k, v := range out.Resources[i].Fields {
				merged[k] = v
			}
			for k, v := range d.Fields {
				merged[k] = v
			}
			op := Updated
			if equalFields(merged, out.Resources[i].Fields) {
				op = Unchanged
			}
			out.Resources[i].Fields = merged
			out.Resources[i].Action = Create
			changes = append(changes, Change{d.Kind, d.Name, op})
		case Delete:
			if exists {
				out.Resources[i].Action = Delete
			} else {
				index[key(d)] = len(out.Resources)
				out.Resources = append(out.Resources, d)
			}
			changes = append(changes, Change{d.Kind, d.Name, Removed})
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return Spec{}, nil, &ConflictError{Problems: problems}
	}
	return out, changes, nil
}

func opFor(a Action) Op {
	if a == Delete {
		return Removed
	}
	return Added
}

func equalFields(a, b map[string]any) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return reflect.DeepEqual(a, b)
}
