package bdd_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	"github.com/axonops/axai/internal/cli"
	"github.com/axonops/axai/internal/config"
	"github.com/axonops/axai/internal/execx"
	"github.com/axonops/axai/internal/execx/exectest"
	"github.com/axonops/axai/internal/llm"
	"github.com/axonops/axai/internal/llm/llmtest"
	"github.com/axonops/axai/internal/read"
)

const token = "tok-0123456789abcdef"

type world struct {
	dir, org, cluster string
	steps             []llmtest.Step
	provider          *llmtest.Scripted
	exec              *exectest.Fake
	applyFails        bool
	planChanges       bool
	tokenUnset        bool
	out, errOut       bytes.Buffer
	exit              int
}

func spec(kind, name, action string, fields map[string]any) string {
	b, _ := json.Marshal(map[string]any{
		"resources":   []any{map[string]any{"kind": kind, "name": name, "action": action, "fields": fields}},
		"assumptions": []string{"warning threshold set to 70"},
		"unsupported": []string{},
	})
	return string(b)
}

var diskAlert = map[string]any{
	"dashboard": "System", "chart": "Disk usage %", "operator": ">",
	"warning_value": 70, "critical_value": 80, "duration": "15m",
}

// urlSecret is the placeholder name axai gives a URL found in a request.
func urlSecret(u string) string {
	sum := sha256.Sum256([]byte(u))
	return "REQUEST_URL_" + strings.ToUpper(hex.EncodeToString(sum[:3]))
}

func (w *world) reset() {
	w.dir = filepath.Join(os.TempDir(), fmt.Sprintf("axai-bdd-%d", os.Getpid()), strings.ReplaceAll(fmt.Sprint(w), "0x", ""))
	_ = os.RemoveAll(w.dir)
	w.planChanges = true
	w.steps, w.applyFails, w.tokenUnset = nil, false, false
	w.out.Reset()
	w.errOut.Reset()
}

func (w *world) cluster_(org, cluster string) error { w.org, w.cluster = org, cluster; return nil }

func (w *world) modelAlert() error {
	w.steps = []llmtest.Step{llmtest.Text(spec("metric_alert_rule", "High Disk", "create", diskAlert))}
	return nil
}

// persistent scripts the same reply for the first attempt and both repairs.
func persistent(reply string) []llmtest.Step {
	return []llmtest.Step{llmtest.Text(reply), llmtest.Text(reply), llmtest.Text(reply)}
}

func (w *world) modelCounter() error {
	reply := spec("custom_dashboard", "Capacity", "create", map[string]any{"panels": []any{
		map[string]any{"title": "Cluster free space %", "type": "metrics_counter", "query": "100 - avg(host_Disk_UsedPercent{mountpoint='/'})"},
	}})
	w.steps = persistent(reply)
	return nil
}

func (w *world) modelSlackFull() error {
	obj := func(kind, name string, f map[string]any) map[string]any {
		return map[string]any{"kind": kind, "name": name, "action": "create", "fields": f}
	}
	b, _ := json.Marshal(map[string]any{
		"resources": []any{
			obj("custom_dashboard", "Free Space", map[string]any{"panels": []any{
				map[string]any{"title": "Free %", "type": "metrics_counter", "query": "100 - avg(host_Disk_UsedPercent{mountpoint='/'})"}}}),
			obj("metric_alert_rule", "Disk over 80", diskAlert),
			obj("slack_integration", "ops-slack", map[string]any{"webhook_url": "$secret:" + urlSecret("https://hooks.slack.com/services/T0/B0/SECRETVALUE")}),
			obj("alert_route", "disk to slack", map[string]any{"integration_name": "ops-slack", "integration_type": "slack", "type": "metrics", "severity": "error"}),
		},
		"assumptions": []string{},
		"unsupported": []string{},
	})
	w.steps = []llmtest.Step{llmtest.Text(string(b))}
	return nil
}

func (w *world) modelUnsupported(part string) error {
	b, _ := json.Marshal(map[string]any{
		"resources":   []any{map[string]any{"kind": "metric_alert_rule", "name": "x", "action": "create", "fields": diskAlert}},
		"assumptions": []string{}, "unsupported": []string{part},
	})
	w.steps = []llmtest.Step{llmtest.Text(string(b))}
	return nil
}

func (w *world) noFileContains(text string) error {
	return filepath.WalkDir(w.dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if b, _ := os.ReadFile(p); strings.Contains(string(b), text) {
			return fmt.Errorf("%s contains %q", p, text)
		}
		return nil
	})
}

func (w *world) modelNeverSaw(text string) error {
	for _, c := range w.provider.Calls {
		all := c.System
		for _, m := range c.Messages {
			all += m.Content
		}
		if strings.Contains(all, text) {
			return fmt.Errorf("the model request contained %q", text)
		}
	}
	return nil
}

var freeSpacePanels = []any{
	map[string]any{"title": "Free %", "type": "metrics_counter", "query": "100 - avg(host_Disk_UsedPercent{mountpoint='/'})"},
}

func freeSpace(panels []any) string {
	return spec("custom_dashboard", "Free Space", "create", map[string]any{"panels": panels})
}

func (w *world) seedChart() error {
	w.steps = []llmtest.Step{llmtest.Text(freeSpace(freeSpacePanels))}
	w.invoke("", "plan", "Create a counter chart that shows the cluster free space", "--backend", "terraform")
	w.steps = nil
	return w.exitIs(0)
}

func (w *world) modelSameChart() error {
	w.steps = []llmtest.Step{llmtest.Text(freeSpace(freeSpacePanels))}
	return nil
}

func (w *world) modelDifferentChart() error {
	w.steps = persistent(freeSpace([]any{map[string]any{"title": "Mem", "type": "metrics_counter", "query": "avg(host_Memory_UsedPercent)"}}))
	return nil
}

func (w *world) planAlert() error {
	w.invoke("", "plan", "Create critical alert when disk over 80%", "--backend", "terraform")
	return w.exitIs(0)
}

func (w *world) modelUpdatesDuration(d string) error {
	w.steps = []llmtest.Step{llmtest.Text(spec("metric_alert_rule", "High Disk", "update", map[string]any{"duration": d}))}
	return nil
}

func (w *world) runPlanOtherCluster(backend string) error {
	w.out.Reset()
	w.errOut.Reset()
	root := cli.New(w.env(""))
	root.SetArgs([]string{"plan", "Create an alert", "--backend", backend, "--org", w.org, "--cluster", "other", "--cluster-type", "cassandra", "--project", w.dir})
	if err := root.Execute(); err != nil {
		w.exit = cli.ExitCode(err)
		return nil
	}
	w.exit = 0
	return nil
}

func (w *world) noSuccess() error {
	if strings.Contains(w.out.String(), "planned:") {
		return fmt.Errorf("run reported success:\n%s", w.out.String())
	}
	return nil
}

func (w *world) modelKind(kind string) error {
	w.steps = persistent(spec(kind, "x", "create", map[string]any{}))
	return nil
}

func (w *world) modelSlack() error {
	w.steps = persistent(spec("metric_alert_rule", "Slack", "create",
		map[string]any{"dashboard": "System", "chart": "CPU", "operator": ">", "warning_value": 1, "critical_value": 2,
			"duration": "5m", "description": "https://hooks.slack.com/services/T000/B000/XXXXXXXX"}))
	return nil
}

func (w *world) modelDelete(name string) error {
	w.steps = []llmtest.Step{llmtest.Text(spec("metric_alert_rule", name, "delete", map[string]any{}))}
	return nil
}

func (w *world) alreadyApplied() error {
	w.planChanges = false
	return nil
}

func (w *world) applyAlwaysFails() error {
	w.applyFails = true
	reply := spec("metric_alert_rule", "High Disk", "create", diskAlert)
	w.steps = append(w.steps, llmtest.Text(reply), llmtest.Text(reply), llmtest.Text(reply))
	return nil
}

func (w *world) env(stdin string) cli.Env {
	h := func(c execx.Cmd) (execx.Result, error) {
		switch {
		case len(c.Args) > 0 && c.Args[0] == "plan":
			if w.planChanges {
				return execx.Result{ExitCode: 2, Stdout: []byte("Plan: 1 to add")}, nil
			}
			return execx.Result{Stdout: []byte("No changes.")}, nil
		case len(c.Args) > 0 && c.Args[0] == "apply":
			if w.applyFails {
				return execx.Result{ExitCode: 1, Stderr: []byte("provider error")}, nil
			}
			return execx.Result{Stdout: []byte("Apply complete")}, nil
		case len(c.Args) > 0 && c.Args[0] == "destroy":
			return execx.Result{}, fmt.Errorf("destroy must never run")
		}
		return execx.Result{Stdout: []byte("changed=1")}, nil
	}
	w.exec = &exectest.Fake{
		Paths:    map[string]string{"terraform": "/bin/terraform", "ansible-playbook": "/bin/ansible-playbook", "ansible-galaxy": "/bin/ansible-galaxy"},
		Handlers: map[string]exectest.Handler{"terraform": h, "ansible-playbook": h, "ansible-galaxy": h},
	}
	w.provider = &llmtest.Scripted{Steps: w.steps}
	vars := map[string]string{"AXONOPS_API_TOKEN": token, "AXAI_PROVIDER": "anthropic", "HOME": w.dir}
	if w.tokenUnset {
		delete(vars, "AXONOPS_API_TOKEN")
	}
	return cli.Env{
		In: strings.NewReader(stdin), Out: &w.out, Err: &w.errOut, Exec: w.exec,
		Getenv:   func(k string) string { return vars[k] },
		Provider: func(config.Config) (llm.Provider, error) { return w.provider, nil },
		Reader:   func(read.Config) (cli.Reader, error) { return fakeReader{}, nil },
	}
}

type fakeReader struct{}

func (fakeReader) Cluster(context.Context, string, string) (read.Context, error) {
	return read.Context{Dashboards: []read.Dashboard{{Name: "System", Charts: []string{"Disk usage %", "CPU"}}}}, nil
}

func (w *world) invoke(stdin string, args ...string) {
	w.out.Reset()
	w.errOut.Reset()
	root := cli.New(w.env(stdin))
	args = append(args, "--org", w.org, "--cluster", w.cluster, "--cluster-type", "cassandra", "--project", w.dir)
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		w.exit = cli.ExitCode(err)
		fmt.Fprintln(&w.errOut, err)
		return
	}
	w.exit = 0
}

func (w *world) runPlan(req, backend string) error {
	w.invoke("", "plan", req, "--backend", backend)
	return nil
}

func (w *world) runApply(req, extra string) error {
	args := append([]string{"apply", req}, strings.Fields(extra)...)
	w.invoke("", args...)
	return nil
}

func (w *world) runApplyAnswer(req, answer string) error {
	w.invoke(answer+"\n", "apply", req)
	return nil
}

func (w *world) runApplyYesAnswer(req, extra, answer string) error {
	args := append([]string{"apply", req, "--backend", "ansible"}, strings.Fields(extra)...)
	w.invoke(answer+"\n", args...)
	return nil
}

func (w *world) exitIs(code int) error {
	if w.exit != code {
		return fmt.Errorf("exit code %d, want %d\nstdout: %s\nstderr: %s", w.exit, code, w.out.String(), w.errOut.String())
	}
	return nil
}

func (w *world) fileContains(name, text string) error {
	b, err := os.ReadFile(filepath.Join(w.dir, name))
	if err != nil {
		return err
	}
	if !strings.Contains(string(b), text) {
		return fmt.Errorf("%s does not contain %q:\n%s", name, text, b)
	}
	return nil
}

func (w *world) noToken() error {
	return filepath.WalkDir(w.dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, _ := os.ReadFile(p)
		if strings.Contains(string(b), token) {
			return fmt.Errorf("%s contains the API token", p)
		}
		return nil
	})
}

func (w *world) nothingWritten() error {
	if _, err := os.Stat(w.dir); !os.IsNotExist(err) {
		return fmt.Errorf("%s exists", w.dir)
	}
	return nil
}

func (w *world) stateIs(status string) error {
	b, err := os.ReadFile(filepath.Join(w.dir, "state.json"))
	if err != nil {
		return err
	}
	if !strings.Contains(string(b), `"status": "`+status+`"`) {
		return fmt.Errorf("state is %s, want %s", b, status)
	}
	return nil
}

func (w *world) noAPIToken() error { w.tokenUnset = true; return nil }

func (w *world) backendPlanNotRun() error {
	for _, name := range []string{"terraform", "tofu", "ansible-playbook"} {
		for _, c := range w.exec.CallsTo(name) {
			if len(c.Args) > 0 && c.Args[0] == "plan" {
				return fmt.Errorf("%s plan ran", name)
			}
		}
	}
	return nil
}

func (w *world) warningContains(text string) error {
	if !strings.Contains(w.errOut.String(), text) {
		return fmt.Errorf("stderr does not contain %q:\n%s", text, w.errOut.String())
	}
	return nil
}

func (w *world) outputContains(text string) error {
	if !strings.Contains(w.out.String(), text) {
		return fmt.Errorf("output lacks %q:\n%s", text, w.out.String())
	}
	return nil
}

func (w *world) nothingDestroyed() error {
	for _, c := range w.exec.Calls {
		for _, a := range c.Args {
			if a == "destroy" || a == "apply" {
				return fmt.Errorf("unexpected %s call", a)
			}
		}
	}
	return nil
}

func (w *world) fixerAsked(max int) error {
	if n := len(w.provider.Calls) - 1; n != max {
		return fmt.Errorf("model asked to fix %d times, want %d", n, max)
	}
	return nil
}

func TestFeatures(t *testing.T) {
	suite := godog.TestSuite{
		Name: "axai",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			w := &world{}
			sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) { w.reset(); return ctx, nil })
			sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
				_ = os.RemoveAll(w.dir)
				return ctx, nil
			})
			sc.Step(`^an AxonOps organisation "([^"]*)" with a cassandra cluster "([^"]*)"$`, w.cluster_)
			sc.Step(`^the model returns a metric alert rule for disk usage above 80 percent$`, w.modelAlert)
			sc.Step(`^the model returns a counter chart for cluster free space$`, w.modelCounter)
			sc.Step(`^the model returns a free-space dashboard, an alert, a Slack integration and a route$`, w.modelSlackFull)
			sc.Step(`^the model reports that "([^"]*)" is unsupported$`, w.modelUnsupported)
			sc.Step(`^no generated file contains "([^"]*)"$`, w.noFileContains)
			sc.Step(`^the model never saw "([^"]*)"$`, w.modelNeverSaw)
			sc.Step(`^the project already has a free-space counter chart$`, w.seedChart)
			sc.Step(`^the model returns the same free-space counter chart again$`, w.modelSameChart)
			sc.Step(`^the model returns a different free-space counter chart with the same name$`, w.modelDifferentChart)
			sc.Step(`^I plan that alert$`, w.planAlert)
			sc.Step(`^the model updates the alert duration to "([^"]*)"$`, w.modelUpdatesDuration)
			sc.Step(`^I run axai plan against cluster "other" with backend "([^"]*)"$`, func(b string) error { return w.runPlanOtherCluster(b) })
			sc.Step(`^the output contains nothing about success$`, w.noSuccess)
			sc.Step(`^the model returns a resource of kind "([^"]*)"$`, w.modelKind)
			sc.Step(`^the model returns a Slack integration with an inline webhook URL$`, w.modelSlack)
			sc.Step(`^the model returns a delete of the alert rule "([^"]*)"$`, w.modelDelete)
			sc.Step(`^the request was already applied$`, w.alreadyApplied)
			sc.Step(`^the apply always fails$`, w.applyAlwaysFails)
			sc.Step(`^I run axai plan "([^"]*)" with backend "([^"]*)"$`, w.runPlan)
			sc.Step(`^I run axai apply "([^"]*)" with "(--yes[^"]*)"$`, w.runApply)
			sc.Step(`^I run axai apply "([^"]*)" and answer "([^"]*)"$`, w.runApplyAnswer)
			sc.Step(`^I run axai apply "([^"]*)" with "(--yes)" and answer "([^"]*)"$`, w.runApplyYesAnswer)
			sc.Step(`^the exit code is (\d+)$`, w.exitIs)
			sc.Step(`^the project contains "([^"]*)" with resource "([^"]*)"$`, w.fileContains)
			sc.Step(`^the project contains "versions.tf" pinning provider "([^"]*)"$`, func(p string) error { return w.fileContains("versions.tf", p) })
			sc.Step(`^the project contains "([^"]*)" with variable "([^"]*)"$`, w.fileContains)
			sc.Step(`^the project contains "site.yml" using role "([^"]*)"$`, func(r string) error { return w.fileContains("site.yml", r) })
			sc.Step(`^no generated file contains the API token$`, w.noToken)
			sc.Step(`^nothing is written to the project directory$`, w.nothingWritten)
			sc.Step(`^the project state is "([^"]*)"$`, w.stateIs)
			sc.Step(`^the output contains "([^"]*)"$`, w.outputContains)
			sc.Step(`^no AxonOps API token is set$`, w.noAPIToken)
			sc.Step(`^the backend plan was not run$`, w.backendPlanNotRun)
			sc.Step(`^the warning contains "([^"]*)"$`, w.warningContains)
			sc.Step(`^nothing is destroyed$`, w.nothingDestroyed)
			sc.Step(`^the model was asked to fix exactly (\d+) times$`, w.fixerAsked)
		},
		Options: &godog.Options{Format: "pretty", Paths: []string{"features"}, TestingT: t, Strict: true},
	}
	if suite.Run() != 0 {
		t.Fatal("feature failures")
	}
}
