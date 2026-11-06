// Package execx runs external programs behind an interface so callers can be
// tested without spawning them.
package execx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// SecretEnv names credentials that Ansible, git and their plugins never
// need, so child processes do not inherit them.
var SecretEnv = []string{
	"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "GEMINI_API_KEY", "OLLAMA_API_KEY", "LLAMACPP_API_KEY",
	"AXAI_API_KEY", "AXAI_API_KEY_FILE",
}

// Cmd describes a command to run.
type Cmd struct {
	Name   string
	Args   []string
	Dir    string
	Env    []string  // KEY=VALUE pairs added to the parent environment
	Unset  []string  // parent environment variables removed before Env is applied
	Stdout io.Writer // optional live copy of stdout
}

// Result is the outcome of a command.
type Result struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

// Runner runs external programs.
type Runner interface {
	Run(ctx context.Context, c Cmd) (Result, error)
	LookPath(name string) (string, error)
}

// OS runs real processes.
type OS struct{}

// LookPath finds name on PATH.
func (OS) LookPath(name string) (string, error) { return exec.LookPath(name) }

// Run runs c as a real process.
func (OS) Run(ctx context.Context, c Cmd) (Result, error) {
	cmd := exec.CommandContext(ctx, c.Name, c.Args...)
	cmd.Dir = c.Dir
	cmd.Env = append(environ(c.Unset), c.Env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	if c.Stdout != nil {
		cmd.Stdout = io.MultiWriter(&stdout, c.Stdout)
	}
	cmd.Stderr = &stderr

	// Ansible forks workers and ssh processes that inherit the output pipes, so
	// killing only the direct child would leave Wait blocked. Run the child in
	// a new session (which is also a new process group) and SIGTERM the whole
	// group so Ansible can stop its workers; WaitDelay force-kills and closes
	// the pipes if they linger. The new session has no controlling terminal, so
	// an ssh password or host-key prompt fails at once instead of stopping the
	// background group with SIGTTIN.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM) }
	cmd.WaitDelay = 10 * time.Second

	err := cmd.Run()
	res := Result{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}
	if ctx.Err() != nil {
		return res, ctx.Err()
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		res.ExitCode = ee.ExitCode()
		return res, nil
	}
	if err != nil {
		return res, fmt.Errorf("run %s: %w", c.Name, err)
	}
	return res, nil
}

func environ(unset []string) []string {
	env := os.Environ()
	if len(unset) == 0 {
		return env
	}
	drop := make(map[string]bool, len(unset))
	for _, k := range unset {
		drop[k] = true
	}
	out := env[:0:0]
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		if !drop[k] {
			out = append(out, kv)
		}
	}
	return out
}
