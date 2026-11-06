// Package state records the outcome of a run in <project>/state.json.
package state

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/axonops/axai/internal/spec"
)

// Run statuses.
const (
	StatusPlanned   = "planned"
	StatusGenerated = "generated"
	StatusApplied   = "applied"
	StatusFailed    = "failed"
)

// State is the content of state.json.
type State struct {
	Status    string    `json:"status"`
	Backend   string    `json:"backend"`
	Request   string    `json:"request"`
	Resources int       `json:"resources"`
	Updated   time.Time `json:"updated"`
	Fixes     []Fix     `json:"fixes,omitempty"`
}

// Fix is one model patch applied after a failure.
type Fix struct {
	Attempt int      `json:"attempt"`
	Error   string   `json:"error"`
	Files   []string `json:"files"`
}

const file = "state.json"

// Save writes st into dir.
func Save(dir string, st State) error {
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, file), append(b, '\n'), 0o600)
}

// Load reads dir's state; found is false when none exists.
func Load(dir string) (st State, found bool, err error) {
	b, err := os.ReadFile(filepath.Join(dir, file))
	if errors.Is(err, fs.ErrNotExist) {
		return State{}, false, nil
	}
	if err != nil {
		return State{}, false, err
	}
	return st, true, json.Unmarshal(b, &st)
}

const specFile = "spec.json"

// SaveSpec writes the project's cumulative spec. It holds secret references
// ($secret:NAME), never secret values.
func SaveSpec(dir string, s spec.Spec) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, specFile), append(b, '\n'), 0o600)
}

// LoadSpec reads the project's cumulative spec; found is false for a new project.
func LoadSpec(dir string) (s spec.Spec, found bool, err error) {
	b, err := os.ReadFile(filepath.Join(dir, specFile))
	if errors.Is(err, fs.ErrNotExist) {
		return spec.Spec{}, false, nil
	}
	if err != nil {
		return spec.Spec{}, false, err
	}
	return s, true, json.Unmarshal(b, &s)
}
