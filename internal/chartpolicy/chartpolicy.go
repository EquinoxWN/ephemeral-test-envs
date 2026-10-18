// Package chartpolicy checks rendered Kubernetes manifests against the environment's safety rules.
package chartpolicy

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

// MaxConfigBytes keeps ConfigMaps well under the 1 MiB object limit.
const MaxConfigBytes = 900 << 10

// Violation is one broken rule on one object.
type Violation struct {
	Object string `json:"object"`
	Rule   string `json:"rule"`
	Detail string `json:"detail"`
}

// String formats a violation for the console.
func (v Violation) String() string { return v.Object + ": " + v.Rule + ": " + v.Detail }

type obj = map[string]any

// Summary counts what was checked.
type Summary struct {
	Objects    int            `json:"objects"`
	Kinds      map[string]int `json:"kinds"`
	Containers int            `json:"containers"`
	Violations []Violation    `json:"violations"`
}

// Check parses a multi-document YAML stream and applies every rule.
func Check(r io.Reader) (Summary, error) {
	s := Summary{Kinds: map[string]int{}}
	dec := yaml.NewDecoder(io.LimitReader(r, 8<<20))
	netpol := false
	for {
		var o obj
		err := dec.Decode(&o)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return s, fmt.Errorf("parse manifests: %w", err)
		}
		if o == nil {
			continue
		}
		kind, _ := o["kind"].(string)
		name, _ := get(o, "metadata", "name").(string)
		id := kind + "/" + name
		s.Objects++
		s.Kinds[kind]++
		add := func(rule, format string, a ...any) {
			s.Violations = append(s.Violations, Violation{id, rule, fmt.Sprintf(format, a...)})
		}
		switch kind {
		case "Deployment", "Job", "StatefulSet", "DaemonSet":
			pod, _ := get(o, "spec", "template", "spec").(obj)
			s.Containers += checkPod(pod, kind == "Deployment", add)
		case "Pod":
			pod, _ := o["spec"].(obj)
			s.Containers += checkPod(pod, false, add)
		case "Service":
			if t, _ := get(o, "spec", "type").(string); t != "" && t != "ClusterIP" {
				add("service-type", "type %s exposes the service outside the cluster", t)
			}
		case "NetworkPolicy":
			netpol = true
		case "ConfigMap":
			size := 0
			data, _ := o["data"].(obj)
			for _, v := range data {
				if str, ok := v.(string); ok {
					size += len(str)
				}
			}
			if size > MaxConfigBytes {
				add("configmap-size", "%d bytes of data, limit %d", size, MaxConfigBytes)
			}
		}
	}
	if s.Objects == 0 {
		return s, errors.New("no manifests found")
	}
	if !netpol {
		s.Violations = append(s.Violations, Violation{"chart", "network-policy", "no NetworkPolicy restricts traffic"})
	}
	return s, nil
}

// checkPod applies the pod and container rules and returns the number of containers seen.
func checkPod(pod obj, long bool, add func(rule, format string, a ...any)) int {
	if pod == nil {
		add("pod-spec", "missing pod spec")
		return 0
	}
	if pod["automountServiceAccountToken"] != false {
		add("service-account-token", "automountServiceAccountToken must be false")
	}
	for _, k := range []string{"hostNetwork", "hostPID", "hostIPC"} {
		if pod[k] == true {
			add("host-namespace", "%s is true", k)
		}
	}
	vols, _ := pod["volumes"].([]any)
	for _, v := range vols {
		if vm, _ := v.(obj); vm != nil && vm["hostPath"] != nil {
			add("host-path", "volume %v mounts a host path", vm["name"])
		}
	}
	if get(pod, "securityContext", "runAsNonRoot") != true {
		add("run-as-non-root", "pod securityContext.runAsNonRoot must be true")
	}
	if get(pod, "securityContext", "seccompProfile", "type") != "RuntimeDefault" {
		add("seccomp", "pod seccompProfile must be RuntimeDefault")
	}
	n := 0
	for _, field := range []string{"initContainers", "containers"} {
		list, _ := pod[field].([]any)
		for _, c := range list {
			cm, _ := c.(obj)
			if cm == nil {
				continue
			}
			n++
			checkContainer(cm, long && field == "containers", add)
		}
	}
	if n == 0 {
		add("containers", "pod has no containers")
	}
	return n
}

// checkContainer applies image, privilege, resource and probe rules to one container.
func checkContainer(c obj, long bool, add func(rule, format string, a ...any)) {
	name, _ := c["name"].(string)
	image, _ := c["image"].(string)
	local := strings.HasPrefix(image, "etenv/") && c["imagePullPolicy"] == "Never"
	if !strings.Contains(image, "@sha256:") && !local {
		add("image-pinned", "container %s image %q is neither digest-pinned nor a local etenv/ image with imagePullPolicy Never", name, image)
	}
	sc, _ := c["securityContext"].(obj)
	if get(sc, "allowPrivilegeEscalation") != false {
		add("privilege-escalation", "container %s must set allowPrivilegeEscalation: false", name)
	}
	if get(sc, "privileged") == true {
		add("privileged", "container %s is privileged", name)
	}
	if get(sc, "readOnlyRootFilesystem") != true {
		add("read-only-root", "container %s must set readOnlyRootFilesystem: true", name)
	}
	drop, _ := get(sc, "capabilities", "drop").([]any)
	if !slices.Contains(drop, any("ALL")) {
		add("capabilities", "container %s must drop ALL capabilities", name)
	}
	for _, kind := range []string{"requests", "limits"} {
		for _, res := range []string{"cpu", "memory"} {
			if get(c, "resources", kind, res) == nil {
				add("resources", "container %s has no %s.%s", name, kind, res)
			}
		}
	}
	if long {
		for _, p := range []string{"readinessProbe", "livenessProbe"} {
			if c[p] == nil {
				add("probes", "container %s has no %s", name, p)
			}
		}
	}
}

// get walks nested maps and returns nil when a key is missing.
func get(o obj, path ...string) any {
	var cur any = o
	for _, p := range path {
		m, ok := cur.(obj)
		if !ok {
			return nil
		}
		cur = m[p]
	}
	return cur
}
