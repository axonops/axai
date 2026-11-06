// Package cli wires the axai commands.
package cli

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/axonops/axai/internal/compose"
	"github.com/axonops/axai/internal/config"
	"github.com/axonops/axai/internal/design"
	"github.com/axonops/axai/internal/execx"
	"github.com/axonops/axai/internal/gitrepo"
	"github.com/axonops/axai/internal/guard"
	"github.com/axonops/axai/internal/llm"
	"github.com/axonops/axai/internal/llm/factory"
	"github.com/axonops/axai/internal/lock"
	"github.com/axonops/axai/internal/pipeline"
	"github.com/axonops/axai/internal/read"
	"github.com/axonops/axai/internal/redact"
	"github.com/axonops/axai/internal/runner"
	"github.com/axonops/axai/internal/spec"
	"github.com/axonops/axai/internal/state"
	"github.com/axonops/axai/internal/structured"
)

// Process exit codes.
const (
	ExitOK         = 0
	ExitInput      = 1
	ExitValidation = 2
	ExitApply      = 3
	ExitRuntime    = 4
)

// ExitError carries a process exit code.
type ExitError struct {
	Code int
	Err  error
}

func (e *ExitError) Error() string {
	if e.Err == nil {
		return fmt.Sprintf("exit %d", e.Code)
	}
	return e.Err.Error()
}

func (e *ExitError) Unwrap() error { return e.Err }

// ExitCode maps an error to a process exit code.
func ExitCode(err error) int {
	var ee *ExitError
	if errors.As(err, &ee) {
		return ee.Code
	}
	return ExitInput
}

// Env is everything the commands take from outside: streams, environment and
// the seams tests replace.
type Env struct {
	In       io.Reader
	Out      io.Writer
	Err      io.Writer
	Getenv   func(string) string
	Exec     execx.Runner
	Provider func(config.Config) (llm.Provider, error) // nil builds from config
	Reader   func(read.Config) (Reader, error)         // nil uses the AxonOps API
}

// Reader reads cluster context.
type Reader interface {
	Cluster(ctx context.Context, clusterType, cluster string) (read.Context, error)
}

// DefaultEnv is the real process environment.
func DefaultEnv() Env {
	return Env{In: os.Stdin, Out: os.Stdout, Err: os.Stderr, Getenv: os.Getenv, Exec: execx.OS{}}
}

type flags struct {
	org, cluster, clusterType, backend, project string
	provider, configPath                        string
	yes, git, noColours                         bool
	maxFixes                                    int
}

// New builds the root command.
func New(env Env) *cobra.Command {
	var f flags
	root := &cobra.Command{
		Use:           "axai",
		Short:         "Generate and apply AxonOps configuration from a natural-language request",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetOut(env.Out)
	root.SetErr(env.Err)
	p := root.PersistentFlags()
	p.StringVar(&f.org, "org", "", "AxonOps organisation (or AXONOPS_ORG)")
	p.StringVar(&f.cluster, "cluster", "", "cluster name (or AXONOPS_CLUSTER)")
	p.StringVar(&f.clusterType, "cluster-type", "", "cassandra|kafka|dse (or AXONOPS_CLUSTER_TYPE)")
	p.StringVar(&f.backend, "backend", "terraform", "ansible|terraform")
	p.StringVar(&f.project, "project", "", "project directory (default .axai/<hash>)")
	p.StringVar(&f.provider, "model", "", "model provider (or AXAI_PROVIDER)")
	p.StringVar(&f.configPath, "config", "", "config file")
	p.BoolVar(&f.noColours, "no-colours", false, "disable colour output")

	plan := &cobra.Command{
		Use: "plan <request>", Short: "Generate the project and show the plan", Args: cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error { return run(c.Context(), env, f, args, false) },
	}
	apply := &cobra.Command{
		Use: "apply <request>", Aliases: []string{"run"}, Short: "Plan and apply", Args: cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error { return run(c.Context(), env, f, args, true) },
	}
	apply.Flags().BoolVar(&f.yes, "yes", false, "skip plan confirmation (deletes still ask)")
	apply.Flags().IntVar(&f.maxFixes, "max-fixes", 3, "maximum fixer iterations")
	apply.Flags().BoolVar(&f.git, "git", false, "commit the project to git after a successful apply")
	status := &cobra.Command{
		Use: "status", Short: "Show the last run of a project", Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error { return statusCmd(env, f) },
	}
	doctor := &cobra.Command{
		Use: "doctor", Short: "Check prerequisites", Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error { return doctorCmd(env) },
	}
	root.AddCommand(plan, apply, status, doctor)
	return root
}

func exit(code int, format string, a ...any) error {
	return &ExitError{Code: code, Err: fmt.Errorf(format, a...)}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func run(ctx context.Context, env Env, f flags, args []string, apply bool) error {
	request := strings.TrimSpace(strings.Join(args, " "))
	if request == "" {
		return exit(ExitInput, "empty request")
	}
	// URLs in a request are treated as secrets (webhooks carry credentials):
	// the model and the saved project see only the placeholder name.
	request, urlSecrets := extractURLs(request)
	f.org = firstNonEmpty(f.org, env.Getenv("AXONOPS_ORG"))
	f.cluster = firstNonEmpty(f.cluster, env.Getenv("AXONOPS_CLUSTER"))
	f.clusterType = strings.ToLower(firstNonEmpty(f.clusterType, env.Getenv("AXONOPS_CLUSTER_TYPE")))
	for name, v := range map[string]string{"--org (AXONOPS_ORG)": f.org, "--cluster (AXONOPS_CLUSTER)": f.cluster, "--cluster-type (AXONOPS_CLUSTER_TYPE)": f.clusterType} {
		if v == "" {
			return exit(ExitInput, "%s is required", name)
		}
	}
	backend := spec.Backend(f.backend)
	if !backend.Valid() {
		return exit(ExitInput, "--backend must be ansible or terraform")
	}
	token := env.Getenv("AXONOPS_API_TOKEN")
	offline := token == ""
	if offline && apply {
		return exit(ExitInput, "set AXONOPS_API_TOKEN")
	}
	if offline {
		fmt.Fprintln(env.Err, "warning: AXONOPS_API_TOKEN is not set; planning offline: no cluster context, backend plan skipped")
	}
	red := &redact.Redactor{}
	if !offline {
		red.Add(token)
	}
	out := red.Writer(env.Out)
	defer func() { _ = out.Flush() }()

	cfg, err := config.Load(f.configPath, env.Getenv)
	if err != nil {
		return exit(ExitInput, "%v", err)
	}
	cfg.Override(config.ProviderConfig{Provider: f.provider})
	var provider llm.Provider
	if env.Provider != nil {
		provider, err = env.Provider(cfg)
	} else {
		provider, err = factory.New(cfg.ForStage("design"), env.Getenv)
	}
	if err != nil {
		return exit(ExitInput, "%v", err)
	}

	var cctx read.Context
	if !offline {
		rd, err := newReader(env, f.org, token)
		if err != nil {
			return exit(ExitInput, "%v", err)
		}
		if cctx, err = rd.Cluster(ctx, f.clusterType, f.cluster); err != nil {
			return exit(ExitRuntime, "read cluster context: %v", err)
		}
	}
	dir := f.project
	if dir == "" {
		dir = filepath.Join(".axai", hash(f.org, f.cluster, f.clusterType, f.backend))
	}
	existing, _, err := state.LoadSpec(dir)
	if err != nil {
		return exit(ExitRuntime, "read project spec: %v", err)
	}
	in := design.Input{Request: request, Org: f.org, Cluster: f.cluster, ClusterType: f.clusterType, Backend: backend, Context: cctx, Secrets: sortedKeys(urlSecrets), Existing: existing}
	delta, err := design.Generate(ctx, provider, in)
	if err != nil {
		return designErr(err)
	}
	s, changes, err := spec.Merge(existing, delta)
	if err != nil {
		return &ExitError{Code: ExitValidation, Err: err}
	}
	for _, c := range changes {
		fmt.Fprintln(out, c)
	}
	for _, a := range s.Assumptions {
		fmt.Fprintf(out, "assumption: %s\n", a)
	}

	secretEnv, err := secretEnv(env, s, backend, red, urlSecrets)
	if err != nil {
		return exit(ExitInput, "%v", err)
	}
	opts := pipeline.Options{
		Offline: offline, Dir: dir, Request: request, Spec: s, Out: out, Yes: f.yes, MaxFixes: f.maxFixes,
		Confirm: func(prompt string) (bool, error) { return ask(env, prompt) },
		Reviser: func(ctx context.Context, cur spec.Spec, failure string) (spec.Spec, error) {
			return design.Revise(ctx, provider, in, cur, red.String(failure))
		},
		Runner: runner.Runner{
			Exec: env.Exec, Backend: backend, Dir: dir, Log: nil,
			Env: append(axonopsEnv(env, f, token), secretEnv...),
		},
	}
	if f.git {
		repo := gitrepo.Repo{Exec: env.Exec, Dir: dir, Log: env.Err}
		opts.Commit = func(ctx context.Context, msg string) {
			if err := repo.Commit(ctx, msg); err != nil {
				fmt.Fprintln(env.Err, "git:", err)
			}
		}
	}

	var outcome pipeline.Outcome
	if apply {
		outcome, err = pipeline.Apply(ctx, opts)
	} else {
		outcome, err = pipeline.Plan(ctx, opts)
	}
	if err != nil {
		return pipelineErr(err, red)
	}
	fmt.Fprintf(out, "%s: %s\n", outcome, dir)
	return nil
}

func newReader(env Env, org, token string) (Reader, error) {
	cfg := read.Config{
		BaseURL: env.Getenv("AXONOPS_BASE_URL"), Org: org, Token: token,
		TokenType: tokenType(env),
	}
	if env.Reader != nil {
		return env.Reader(cfg)
	}
	return read.New(cfg)
}

func designErr(err error) error {
	var ge *guard.Error
	var se *structured.StageError
	var ue *design.UnsupportedError
	switch {
	case errors.As(err, &ge), errors.As(err, &se), errors.As(err, &ue):
		return &ExitError{Code: ExitValidation, Err: err}
	}
	return &ExitError{Code: ExitRuntime, Err: err}
}

func pipelineErr(err error, red *redact.Redactor) error {
	msg := errors.New(red.String(err.Error()))
	var ge *guard.Error
	var lk *lock.BusyError
	switch {
	case errors.Is(err, pipeline.ErrApplyFailed):
		return &ExitError{Code: ExitApply, Err: msg}
	case errors.As(err, &ge):
		return &ExitError{Code: ExitValidation, Err: msg}
	case errors.As(err, &lk):
		return &ExitError{Code: ExitInput, Err: msg}
	case errors.Is(err, runner.ErrToolMissing):
		return &ExitError{Code: ExitInput, Err: msg}
	}
	return &ExitError{Code: ExitRuntime, Err: msg}
}

func hash(parts ...string) string {
	h := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(h[:])[:12]
}

func ask(env Env, prompt string) (bool, error) {
	fmt.Fprintf(env.Out, "%s Type yes to continue: ", prompt)
	line, err := bufio.NewReader(env.In).ReadString('\n')
	if err != nil && line == "" {
		return false, nil
	}
	return strings.EqualFold(strings.TrimSpace(line), "yes"), nil
}

// axonopsEnv maps the axai environment onto what Terraform and Ansible read.
func axonopsEnv(env Env, f flags, token string) []string {
	var e []string
	if token != "" {
		e = append(e, "AXONOPS_API_KEY="+token, "AXONOPS_API_TOKEN="+token)
	}
	e = append(e,
		"AXONOPS_ORG="+f.org, "AXONOPS_CLUSTER="+f.cluster, "AXONOPS_CLUSTER_TYPE="+f.clusterType,
	)
	if tt := tokenType(env); tt != "" {
		e = append(e, "AXONOPS_TOKEN_TYPE="+tt)
	}
	if base := env.Getenv("AXONOPS_BASE_URL"); base != "" {
		e = append(e, "AXONOPS_URL="+base)
		if u, err := url.Parse(base); err == nil && u.Host != "" {
			e = append(e, "AXONOPS_HOST="+u.Host, "AXONOPS_PROTOCOL="+u.Scheme)
		}
	}
	return e
}

// secretEnv checks that every $secret: reference has a value and passes it to
// the backend, as TF_VAR_<name> for Terraform.
func secretEnv(env Env, s spec.Spec, b spec.Backend, red *redact.Redactor, fromRequest map[string]string) ([]string, error) {
	var out []string
	for _, n := range compose.Secrets(s) {
		v := fromRequest[n]
		if v == "" {
			v = env.Getenv(n)
		}
		if v == "" {
			return nil, fmt.Errorf("the project needs a secret: set %s in the environment, or repeat the URL in the request", n)
		}
		red.Add(v)
		if b == spec.Terraform {
			out = append(out, "TF_VAR_"+strings.ToLower(n)+"="+v)
		} else {
			out = append(out, n+"="+v)
		}
	}
	return out, nil
}

func statusCmd(env Env, f flags) error {
	if f.project == "" {
		return exit(ExitInput, "--project is required")
	}
	st, found, err := state.Load(f.project)
	if err != nil {
		return exit(ExitRuntime, "%v", err)
	}
	if !found {
		return exit(ExitInput, "no state in %s", f.project)
	}
	fmt.Fprintf(env.Out, "status: %s\nbackend: %s\nresources: %d\nupdated: %s\nrequest: %s\nfixes: %d\n",
		st.Status, st.Backend, st.Resources, st.Updated.Format("2006-01-02T15:04:05Z"), st.Request, len(st.Fixes))
	return nil
}

func doctorCmd(env Env) error {
	missing := false
	check := func(label string, names ...string) {
		for _, n := range names {
			if p, err := env.Exec.LookPath(n); err == nil {
				fmt.Fprintf(env.Out, "ok       %s: %s\n", label, p)
				return
			}
		}
		fmt.Fprintf(env.Out, "missing  %s (%s)\n", label, strings.Join(names, " or "))
		missing = true
	}
	check("terraform backend", "terraform", "tofu")
	check("ansible backend", "ansible-playbook")
	check("ansible collections", "ansible-galaxy")
	check("git (for --git)", "git")
	for _, v := range []string{"AXONOPS_API_TOKEN", "AXONOPS_ORG"} {
		if env.Getenv(v) == "" {
			fmt.Fprintf(env.Out, "unset    %s\n", v)
			missing = true
		} else {
			fmt.Fprintf(env.Out, "ok       %s is set\n", v)
		}
	}
	if missing {
		return &ExitError{Code: ExitInput}
	}
	return nil
}

// tokenType returns AXONOPS_TOKEN_TYPE, or AxonApi for a self-hosted server
// (AXONOPS_BASE_URL set) and Bearer for SaaS.
func tokenType(env Env) string {
	if tt := env.Getenv("AXONOPS_TOKEN_TYPE"); tt != "" {
		return tt
	}
	if env.Getenv("AXONOPS_BASE_URL") != "" {
		return "AxonApi"
	}
	return "Bearer"
}

var urlRE = regexp.MustCompile(`https?://[^\s"'<>]+`)

// extractURLs replaces each URL in the request with a placeholder named from
// the URL's hash (REQUEST_URL_1A2B3C) and returns the placeholder-to-URL map.
// The same URL always gets the same name, so projects built over several
// runs never mix two URLs under one name.
func extractURLs(request string) (string, map[string]string) {
	found := map[string]string{}
	out := urlRE.ReplaceAllStringFunc(request, func(u string) string {
		clean := strings.TrimRight(u, ".,;)")
		sum := sha256.Sum256([]byte(clean))
		name := "REQUEST_URL_" + strings.ToUpper(hex.EncodeToString(sum[:3]))
		found[name] = clean
		return name + strings.TrimPrefix(u, clean)
	})
	return out, found
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
