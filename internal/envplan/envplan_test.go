package envplan

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// cfg returns a valid config rooted in a temp dir.
func cfg(t *testing.T) Config {
	t.Helper()
	root := t.TempDir()
	return Config{PR: 42, Commit: "0123456789abcdef0123456789abcdef01234567", Root: root,
		StateDir: filepath.Join(root, ".tmp", "envs"), NodeImage: DefaultNodeImage, Wait: 4 * time.Minute}
}

func TestValidateRefusesUnsafeValues(t *testing.T) {
	cases := map[string]func(*Config){
		"pr zero":               func(c *Config) { c.PR = 0 },
		"pr too big":            func(c *Config) { c.PR = 10_000_000 },
		"commit with shell":     func(c *Config) { c.Commit = "abc1234; rm -rf /" },
		"commit uppercase":      func(c *Config) { c.Commit = "ABCDEF1" },
		"commit too short":      func(c *Config) { c.Commit = "abc12" },
		"node image by tag":     func(c *Config) { c.NodeImage = "kindest/node:v1.37.0" },
		"node image bad digest": func(c *Config) { c.NodeImage = "kindest/node:v1.37.0@sha256:1234" },
		"wait too short":        func(c *Config) { c.Wait = time.Second },
		"wait too long":         func(c *Config) { c.Wait = time.Hour },
		"no state dir":          func(c *Config) { c.StateDir = "" },
	}
	for name, mutate := range cases {
		c := cfg(t)
		mutate(&c)
		if _, err := Build(c); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestBuildNamesAndOrder(t *testing.T) {
	c := cfg(t)
	p, err := Build(c)
	if err != nil {
		t.Fatal(err)
	}
	if p.Cluster != "etenv-pr-42" || p.Namespace != "pr-42" || p.Tag != "pr-42-0123456" {
		t.Fatalf("names %q %q %q", p.Cluster, p.Namespace, p.Tag)
	}
	var names []string
	for _, s := range p.Up {
		names = append(names, s.Name)
	}
	want := []string{"create cluster", "build api image", "build seeder image", "load api image",
		"load seeder image", "install chart with seed data", "helm test", "list resources"}
	if !slices.Equal(names, want) {
		t.Fatalf("up steps %v", names)
	}
	if !slices.Contains(p.Up[0].Argv, DefaultNodeImage) || !slices.Contains(p.Up[5].Argv, "image.tag=pr-42-0123456") {
		t.Error("node image or image tag missing from argv")
	}
	if p.Teardown[0].Argv[1] != "delete" || !slices.Contains(p.Teardown[0].Argv, "etenv-pr-42") {
		t.Errorf("teardown %v", p.Teardown[0].Argv)
	}
}

func TestEveryKubernetesCallUsesTheEnvironmentsOwnKubeconfig(t *testing.T) {
	c := cfg(t)
	p, _ := Build(c)
	kube := filepath.Join(c.StateDir, "etenv-pr-42.kubeconfig")
	if p.Env["KUBECONFIG"] != kube {
		t.Fatalf("KUBECONFIG %q", p.Env["KUBECONFIG"])
	}
	for _, k := range []string{"HELM_CACHE_HOME", "HELM_CONFIG_HOME", "HELM_DATA_HOME"} {
		if !strings.HasPrefix(p.Env[k], c.StateDir) {
			t.Errorf("%s=%q outside the state dir", k, p.Env[k])
		}
	}
	all := slices.Concat(p.Up, p.Diagnostics, p.Teardown)
	allowed := []string{"kind", "docker", "helm", "kubectl"}
	for _, s := range all {
		if !slices.Contains(allowed, s.Argv[0]) {
			t.Errorf("%s runs %q", s.Name, s.Argv[0])
		}
		if s.Argv[0] == "docker" || slices.Contains(s.Argv, "load") {
			continue
		}
		if !slices.Contains(s.Argv, kube) {
			t.Errorf("%s does not pass --kubeconfig %s: %v", s.Name, kube, s.Argv)
		}
	}
}

// fake records steps and fails the named ones.
type fake struct {
	ran   []string
	fail  map[string]bool
	ctxOK []bool
}

// Run records the call and fails on demand.
func (f *fake) Run(ctx context.Context, s Step, _ map[string]string, _ io.Writer) error {
	f.ran = append(f.ran, s.Name)
	f.ctxOK = append(f.ctxOK, ctx.Err() == nil)
	if f.fail[s.Name] {
		return errors.New("exit status 1")
	}
	return nil
}

// clock returns a fake clock that advances one second per call.
func clock() func() time.Time {
	t := time.Unix(0, 0)
	return func() time.Time { t = t.Add(time.Second); return t }
}

func TestRunPassesAndTearsDown(t *testing.T) {
	p, _ := Build(cfg(t))
	f := &fake{}
	r := Run(context.Background(), p, f, io.Discard, clock())
	if r.Result != "pass" || !r.TornDown || len(r.Steps) != len(p.Up)+1 || f.ran[len(f.ran)-1] != "delete cluster" {
		t.Fatalf("report %+v ran %v", r, f.ran)
	}
	if r.UpSeconds <= 0 || r.TotalSeconds < r.UpSeconds {
		t.Errorf("timings %v %v", r.UpSeconds, r.TotalSeconds)
	}
	if !strings.HasPrefix(r.Annotation(), "::notice") {
		t.Errorf("annotation %q", r.Annotation())
	}
}

func TestRunStopsAtFailureCollectsDiagnosticsAndStillTearsDown(t *testing.T) {
	p, _ := Build(cfg(t))
	f := &fake{fail: map[string]bool{"install chart with seed data": true}}
	var out bytes.Buffer
	r := Run(context.Background(), p, f, &out, clock())
	want := []string{"create cluster", "build api image", "build seeder image", "load api image", "load seeder image",
		"install chart with seed data", "events", "pod logs", "delete cluster"}
	if !slices.Equal(f.ran, want) {
		t.Fatalf("ran %v", f.ran)
	}
	if r.Result != "fail" || r.FailedStep != "install chart with seed data" || !r.TornDown {
		t.Fatalf("report %+v", r)
	}
	if !strings.HasPrefix(r.Annotation(), "::error") || !strings.Contains(out.String(), "failed: exit status 1") {
		t.Errorf("annotation %q / output %q", r.Annotation(), out.String())
	}
}

func TestTeardownRunsWithALiveContextAfterCancellation(t *testing.T) {
	p, _ := Build(cfg(t))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f := &fake{fail: map[string]bool{"create cluster": true}}
	r := Run(ctx, p, f, io.Discard, clock())
	if f.ran[len(f.ran)-1] != "delete cluster" || !f.ctxOK[len(f.ctxOK)-1] {
		t.Fatalf("teardown missing or ran with a dead context: %v %v", f.ran, f.ctxOK)
	}
	if !r.TornDown {
		t.Error("teardown not recorded")
	}
}

func TestFailedTeardownIsReported(t *testing.T) {
	p, _ := Build(cfg(t))
	r := Run(context.Background(), p, &fake{fail: map[string]bool{"delete cluster": true}}, io.Discard, clock())
	if r.Result != "pass" || r.TornDown || !strings.HasPrefix(r.Annotation(), "::error") {
		t.Fatalf("report %+v annotation %q", r, r.Annotation())
	}
}

// TestHelperProcess is the child process for the ExecRunner tests.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("ETENV_HELPER") != "1" {
		return
	}
	_, _ = os.Stdout.WriteString("kube=" + os.Getenv("KUBECONFIG") + "\n")
	if os.Getenv("ETENV_HELPER_FAIL") == "1" {
		os.Exit(3)
	}
	os.Exit(0)
}

func TestExecRunnerPassesEnvironmentAndExitCodes(t *testing.T) {
	t.Setenv("ETENV_HELPER", "1")
	step := Step{Name: "helper", Argv: []string{os.Args[0], "-test.run=^TestHelperProcess$"}}
	var out bytes.Buffer
	if err := (ExecRunner{}).Run(context.Background(), step, map[string]string{"KUBECONFIG": "x.kubeconfig"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "kube=x.kubeconfig") {
		t.Fatalf("child output %q", out.String())
	}
	t.Setenv("ETENV_HELPER_FAIL", "1")
	if err := (ExecRunner{}).Run(context.Background(), step, nil, io.Discard); err == nil {
		t.Fatal("non-zero exit not reported")
	}
	if err := (ExecRunner{}).Run(context.Background(), Step{}, nil, io.Discard); err == nil {
		t.Fatal("empty argv accepted")
	}
}
