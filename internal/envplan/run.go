package envplan

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"
)

// Executor runs one step; tests swap in a fake.
type Executor interface {
	Run(ctx context.Context, s Step, env map[string]string, out io.Writer) error
}

// ExecRunner runs steps as child processes without a shell.
type ExecRunner struct{}

// Run starts argv[0] with the plan's environment added and waits for it.
func (ExecRunner) Run(ctx context.Context, s Step, env map[string]string, out io.Writer) error {
	if len(s.Argv) == 0 {
		return errors.New("empty command")
	}
	cmd := exec.CommandContext(ctx, s.Argv[0], s.Argv[1:]...) // #nosec G204 -- argv built by Build from validated values
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	cmd.Stdout, cmd.Stderr = out, out
	cmd.WaitDelay = 10 * time.Second
	return cmd.Run()
}

// StepResult records one step's outcome and duration.
type StepResult struct {
	Phase   string  `json:"phase"`
	Name    string  `json:"name"`
	Seconds float64 `json:"seconds"`
	OK      bool    `json:"ok"`
	Error   string  `json:"error,omitempty"`
}

// Report is the machine-readable result of one environment run.
type Report struct {
	Cluster      string       `json:"cluster"`
	Namespace    string       `json:"namespace"`
	Tag          string       `json:"tag"`
	Result       string       `json:"result"` // pass or fail
	FailedStep   string       `json:"failed_step,omitempty"`
	TornDown     bool         `json:"torn_down"`
	UpSeconds    float64      `json:"up_seconds"`
	TotalSeconds float64      `json:"total_seconds"`
	Steps        []StepResult `json:"steps"`
}

// Run executes the plan: up steps until one fails, diagnostics on failure, teardown always.
func Run(ctx context.Context, p Plan, ex Executor, out io.Writer, now func() time.Time) Report {
	r := Report{Cluster: p.Cluster, Namespace: p.Namespace, Tag: p.Tag, Result: "pass", TornDown: true}
	start := now()
	step := func(c context.Context, phase string, s Step) bool {
		fmt.Fprintf(out, "==> %s: %s\n", phase, s.Name)
		t := now()
		err := ex.Run(c, s, p.Env, out)
		res := StepResult{Phase: phase, Name: s.Name, Seconds: now().Sub(t).Seconds(), OK: err == nil}
		if err != nil {
			res.Error = err.Error()
			fmt.Fprintf(out, "==> %s failed: %v\n", s.Name, err)
		}
		r.Steps = append(r.Steps, res)
		return err == nil
	}
	for _, s := range p.Up {
		if !step(ctx, "up", s) {
			r.Result, r.FailedStep = "fail", s.Name
			break
		}
	}
	r.UpSeconds = now().Sub(start).Seconds()
	// Diagnostics and teardown must run even when the caller's context is cancelled.
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Minute)
	defer cancel()
	if r.Result == "fail" {
		for _, s := range p.Diagnostics {
			step(cleanup, "diagnostics", s)
		}
	}
	for _, s := range p.Teardown {
		if !step(cleanup, "teardown", s) {
			r.TornDown = false
		}
	}
	r.TotalSeconds = now().Sub(start).Seconds()
	return r
}

// Annotation is the GitHub Actions line that summarizes a report.
func (r Report) Annotation() string {
	if r.Result != "pass" || !r.TornDown {
		return fmt.Sprintf("::error title=ephemeral environment::%s: FAIL at %q, torn down=%t", r.Cluster, r.FailedStep, r.TornDown)
	}
	return fmt.Sprintf("::notice title=ephemeral environment::%s: PASS, up in %.0f s, total %.0f s, torn down=%t",
		r.Cluster, r.UpSeconds, r.TotalSeconds, r.TornDown)
}
