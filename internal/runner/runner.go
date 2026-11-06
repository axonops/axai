// Package runner runs Terraform (or OpenTofu) and Ansible against a
// generated project.
package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"

	"github.com/axonops/axai/internal/execx"
	"github.com/axonops/axai/internal/spec"
)

// Result is the outcome of a plan or apply.
type Result struct {
	Output   string
	Changes  bool // plan: something would change; apply: something changed
	ExitCode int
}

// Runner executes a project's backend tool.
type Runner struct {
	Exec    execx.Runner
	Backend spec.Backend
	Dir     string
	Env     []string // KEY=VALUE added to the child environment
	Log     io.Writer
}

// ErrToolMissing means the backend tool is not installed.
var ErrToolMissing = errors.New("backend tool not found")

func (r Runner) run(ctx context.Context, name string, args ...string) (execx.Result, error) {
	return r.Exec.Run(ctx, execx.Cmd{
		Name: name, Args: args, Dir: r.Dir, Stdout: r.Log,
		Env: append([]string{"TF_IN_AUTOMATION=1", "ANSIBLE_NOCOLOR=1", "TF_INPUT=0"}, r.Env...),
	})
}

func (r Runner) tf() (string, error) {
	for _, n := range []string{"terraform", "tofu"} {
		if p, err := r.Exec.LookPath(n); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("%w: install terraform or tofu", ErrToolMissing)
}

func (r Runner) tool(name string) (string, error) {
	p, err := r.Exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("%w: install %s", ErrToolMissing, name)
	}
	return p, nil
}

func text(res execx.Result) string {
	return strings.TrimSpace(string(res.Stdout) + "\n" + string(res.Stderr))
}

// Init prepares the project: terraform init, or the collection install.
func (r Runner) Init(ctx context.Context) error {
	switch r.Backend {
	case spec.Terraform:
		bin, err := r.tf()
		if err != nil {
			return err
		}
		res, err := r.run(ctx, bin, "init", "-input=false", "-no-color")
		if cerr := cmdErr("init", res, err); cerr != nil {
			return cerr
		}
		// Formatting is cosmetic; a failure must not stop the run.
		_, _ = r.run(ctx, bin, "fmt", "-no-color")
		return nil
	case spec.Ansible:
		bin, err := r.tool("ansible-galaxy")
		if err != nil {
			return err
		}
		res, err := r.run(ctx, bin, "collection", "install", "-r", "requirements.yml", "-p", "collections")
		return cmdErr("collection install", res, err)
	}
	return fmt.Errorf("unknown backend %q", r.Backend)
}

// Plan shows what would change without changing it.
func (r Runner) Plan(ctx context.Context) (Result, error) {
	switch r.Backend {
	case spec.Terraform:
		bin, err := r.tf()
		if err != nil {
			return Result{}, err
		}
		res, err := r.run(ctx, bin, "plan", "-input=false", "-no-color", "-detailed-exitcode", "-out=tfplan")
		if err != nil {
			return Result{}, err
		}
		out := Result{Output: text(res), ExitCode: res.ExitCode}
		switch res.ExitCode {
		case 0:
		case 2:
			out.Changes = true
		default:
			return out, &Failure{Stage: "plan", Output: out.Output}
		}
		return out, nil
	case spec.Ansible:
		bin, err := r.tool("ansible-playbook")
		if err != nil {
			return Result{}, err
		}
		res, err := r.run(ctx, bin, "site.yml", "--check", "--diff")
		if err != nil {
			return Result{}, err
		}
		out := Result{Output: text(res), ExitCode: res.ExitCode}
		if res.ExitCode != 0 {
			return out, &Failure{Stage: "plan", Output: out.Output}
		}
		out.Changes = recapChanged(out.Output) > 0
		return out, nil
	}
	return Result{}, fmt.Errorf("unknown backend %q", r.Backend)
}

// Apply makes the changes.
func (r Runner) Apply(ctx context.Context) (Result, error) {
	switch r.Backend {
	case spec.Terraform:
		bin, err := r.tf()
		if err != nil {
			return Result{}, err
		}
		res, err := r.run(ctx, bin, "apply", "-input=false", "-no-color", "tfplan")
		if err != nil {
			return Result{}, err
		}
		out := Result{Output: text(res), ExitCode: res.ExitCode, Changes: true}
		if res.ExitCode != 0 {
			return out, &Failure{Stage: "apply", Output: out.Output}
		}
		return out, nil
	case spec.Ansible:
		bin, err := r.tool("ansible-playbook")
		if err != nil {
			return Result{}, err
		}
		res, err := r.run(ctx, bin, "site.yml")
		if err != nil {
			return Result{}, err
		}
		out := Result{Output: text(res), ExitCode: res.ExitCode}
		if res.ExitCode != 0 {
			return out, &Failure{Stage: "apply", Output: out.Output}
		}
		out.Changes = recapChanged(out.Output) > 0
		return out, nil
	}
	return Result{}, fmt.Errorf("unknown backend %q", r.Backend)
}

// Failure is a backend command that exited non-zero.
type Failure struct {
	Stage  string
	Output string
}

func (f *Failure) Error() string { return f.Stage + " failed:\n" + f.Output }

func cmdErr(stage string, res execx.Result, err error) error {
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return &Failure{Stage: stage, Output: text(res)}
	}
	return nil
}

var changedRE = regexp.MustCompile(`changed=(\d+)`)

func recapChanged(out string) int {
	total := 0
	for _, m := range changedRE.FindAllStringSubmatch(out, -1) {
		n, _ := strconv.Atoi(m[1])
		total += n
	}
	return total
}
