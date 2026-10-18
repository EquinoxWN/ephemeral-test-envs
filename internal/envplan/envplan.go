// Package envplan builds and runs the lifecycle of one pull request's environment:
// kind cluster, images, Helm release with seed data, checks, and teardown.
package envplan

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"time"
)

// DefaultNodeImage is the kind node image for kind v0.33.0, pinned by digest.
const DefaultNodeImage = "kindest/node:v1.37.0@sha256:a1ed56cfb0e7b93589bdf97c8cd566405a265939e3620fc4f5de89adff580ae5"

var (
	commitRE = regexp.MustCompile(`^[0-9a-f]{7,40}$`)
	digestRE = regexp.MustCompile(`^[a-z0-9./_-]+:[a-z0-9._-]+@sha256:[0-9a-f]{64}$`)
)

// Config describes one environment; every field ends up in argv, never in a shell.
type Config struct {
	PR        int
	Commit    string
	Root      string // repository root with Dockerfile, deploy/chart and deploy/kind
	StateDir  string // kubeconfig and tool homes, inside the repository
	NodeImage string
	Wait      time.Duration
}

// Step is one command.
type Step struct {
	Name string   `json:"name"`
	Argv []string `json:"argv"`
}

// Plan is the ordered lifecycle of an environment.
type Plan struct {
	Cluster     string            `json:"cluster"`
	Namespace   string            `json:"namespace"`
	Tag         string            `json:"tag"`
	Env         map[string]string `json:"env"`
	Up          []Step            `json:"up"`
	Diagnostics []Step            `json:"diagnostics"`
	Teardown    []Step            `json:"teardown"`
}

// Validate refuses values that could escape their argument or point outside the pinned images.
func (c Config) Validate() error {
	switch {
	case c.PR < 1 || c.PR > 9_999_999:
		return fmt.Errorf("pr=%d out of range 1..9999999", c.PR)
	case !commitRE.MatchString(c.Commit):
		return fmt.Errorf("commit %q is not 7 to 40 lowercase hex characters", c.Commit)
	case !digestRE.MatchString(c.NodeImage):
		return fmt.Errorf("node image %q is not pinned by sha256 digest", c.NodeImage)
	case c.Wait < 30*time.Second || c.Wait > 30*time.Minute:
		return fmt.Errorf("wait %s out of range 30s..30m", c.Wait)
	case c.Root == "" || c.StateDir == "":
		return fmt.Errorf("root and state dir are required")
	}
	return nil
}

// Build turns a config into the command plan.
func Build(c Config) (Plan, error) {
	if err := c.Validate(); err != nil {
		return Plan{}, err
	}
	pr := strconv.Itoa(c.PR)
	cluster := "etenv-pr-" + pr
	ns := "pr-" + pr
	tag := "pr-" + pr + "-" + c.Commit[:7]
	kube := filepath.Join(c.StateDir, cluster+".kubeconfig")
	wait := c.Wait.String()
	kctx := "kind-" + cluster
	api, seed := "etenv/api:"+tag, "etenv/seeder:"+tag
	helm := func(args ...string) []string {
		return append([]string{"helm", "--kubeconfig", kube, "--kube-context", kctx}, args...)
	}
	kubectl := func(args ...string) []string {
		return append([]string{"kubectl", "--kubeconfig", kube, "--context", kctx}, args...)
	}
	build := func(target, image string) []string {
		return []string{"docker", "build", "--target", target, "--tag", image,
			"--build-arg", "COMMIT=" + c.Commit, "--label", "org.opencontainers.image.revision=" + c.Commit,
			"--file", filepath.Join(c.Root, "Dockerfile"), c.Root}
	}
	p := Plan{
		Cluster: cluster, Namespace: ns, Tag: tag,
		Env: map[string]string{
			"KUBECONFIG":       kube,
			"HELM_CACHE_HOME":  filepath.Join(c.StateDir, "helm", "cache"),
			"HELM_CONFIG_HOME": filepath.Join(c.StateDir, "helm", "config"),
			"HELM_DATA_HOME":   filepath.Join(c.StateDir, "helm", "data"),
		},
		Up: []Step{
			{"create cluster", []string{"kind", "create", "cluster", "--name", cluster, "--image", c.NodeImage,
				"--config", filepath.Join(c.Root, "deploy", "kind", "cluster.yaml"), "--kubeconfig", kube, "--wait", wait}},
			{"build api image", build("api", api)},
			{"build seeder image", build("seeder", seed)},
			{"load api image", []string{"kind", "load", "docker-image", api, "--name", cluster}},
			{"load seeder image", []string{"kind", "load", "docker-image", seed, "--name", cluster}},
			{"install chart with seed data", helm("install", "app", filepath.Join(c.Root, "deploy", "chart"),
				"--namespace", ns, "--create-namespace", "--set", "image.tag="+tag,
				"--set", "commit="+c.Commit, "--wait", "--timeout", wait)},
			{"helm test", helm("test", "app", "--namespace", ns, "--timeout", wait, "--logs")},
			{"list resources", kubectl("get", "deployments,pods,services,jobs,networkpolicies", "--namespace", ns, "-o", "wide")},
		},
		Diagnostics: []Step{
			{"events", kubectl("get", "events", "--namespace", ns, "--sort-by", ".lastTimestamp")},
			{"pod logs", kubectl("logs", "--namespace", ns, "-l", "app.kubernetes.io/instance=app", "--all-containers", "--tail", "200", "--prefix")},
		},
		Teardown: []Step{
			{"delete cluster", []string{"kind", "delete", "cluster", "--name", cluster, "--kubeconfig", kube}},
		},
	}
	return p, nil
}
