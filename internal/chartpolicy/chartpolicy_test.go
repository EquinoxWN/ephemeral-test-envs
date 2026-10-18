package chartpolicy

import (
	"strings"
	"testing"
)

const good = `
apiVersion: v1
kind: Service
metadata: {name: app-api}
spec: {type: ClusterIP}
---
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata: {name: app-api}
---
apiVersion: v1
kind: ConfigMap
metadata: {name: app-seed}
data: {seed.json: "{}"}
---
apiVersion: apps/v1
kind: Deployment
metadata: {name: app-api}
spec:
  template:
    spec:
      automountServiceAccountToken: false
      securityContext:
        runAsNonRoot: true
        seccompProfile: {type: RuntimeDefault}
      containers:
        - name: api
          image: etenv/api:pr-1-abcdef0
          imagePullPolicy: Never
          securityContext:
            allowPrivilegeEscalation: false
            readOnlyRootFilesystem: true
            capabilities: {drop: [ALL]}
          resources:
            requests: {cpu: 10m, memory: 16Mi}
            limits: {cpu: 100m, memory: 32Mi}
          readinessProbe: {httpGet: {path: /readyz, port: 8080}}
          livenessProbe: {httpGet: {path: /healthz, port: 8080}}
`

func TestGoodManifestsPass(t *testing.T) {
	s, err := Check(strings.NewReader(good))
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Violations) != 0 || s.Objects != 4 || s.Containers != 1 {
		t.Fatalf("summary %+v", s)
	}
}

func TestEachRuleCatchesItsMistake(t *testing.T) {
	cases := []struct{ rule, from, to string }{
		{"run-as-non-root", "runAsNonRoot: true", "runAsNonRoot: false"},
		{"seccomp", "{type: RuntimeDefault}", "{type: Unconfined}"},
		{"service-account-token", "automountServiceAccountToken: false", "automountServiceAccountToken: true"},
		{"image-pinned", "image: etenv/api:pr-1-abcdef0", "image: nginx:latest"},
		{"image-pinned", "imagePullPolicy: Never", "imagePullPolicy: IfNotPresent"},
		{"privilege-escalation", "allowPrivilegeEscalation: false", "allowPrivilegeEscalation: true"},
		{"read-only-root", "readOnlyRootFilesystem: true", "readOnlyRootFilesystem: false"},
		{"capabilities", "drop: [ALL]", "drop: [NET_RAW]"},
		{"resources", "limits: {cpu: 100m, memory: 32Mi}", "limits: {cpu: 100m}"},
		{"probes", "livenessProbe:", "startupProbe:"},
		{"service-type", "type: ClusterIP", "type: NodePort"},
		{"network-policy", "kind: NetworkPolicy", "kind: Secret"},
		{"host-namespace", "automountServiceAccountToken: false", "automountServiceAccountToken: false\n      hostNetwork: true"},
		{"host-path", "automountServiceAccountToken: false", "automountServiceAccountToken: false\n      volumes: [{name: root, hostPath: {path: /}}]"},
		{"privileged", "allowPrivilegeEscalation: false", "allowPrivilegeEscalation: false\n            privileged: true"},
		{"configmap-size", `seed.json: "{}"`, `seed.json: "` + strings.Repeat("x", MaxConfigBytes+1) + `"`},
	}
	for _, c := range cases {
		in := strings.Replace(good, c.from, c.to, 1)
		if in == good {
			t.Fatalf("%s: replacement %q not found", c.rule, c.from)
		}
		s, err := Check(strings.NewReader(in))
		if err != nil {
			t.Fatalf("%s: %v", c.rule, err)
		}
		found := false
		for _, v := range s.Violations {
			found = found || v.Rule == c.rule
		}
		if !found {
			t.Errorf("%s: not reported (got %v)", c.rule, s.Violations)
		}
	}
}

func TestDigestPinnedImagesAreAllowed(t *testing.T) {
	in := strings.Replace(good, "image: etenv/api:pr-1-abcdef0", "image: registry.example/api@sha256:"+strings.Repeat("a", 64), 1)
	in = strings.Replace(in, "imagePullPolicy: Never", "imagePullPolicy: IfNotPresent", 1)
	s, err := Check(strings.NewReader(in))
	if err != nil || len(s.Violations) != 0 {
		t.Fatalf("digest-pinned image refused: %v %v", err, s.Violations)
	}
}

func TestJobsAndPodsAreCheckedWithoutProbes(t *testing.T) {
	pod := `
apiVersion: v1
kind: Pod
metadata: {name: t}
spec:
  containers: [{name: c, image: etenv/seeder:dev}]
`
	s, err := Check(strings.NewReader(pod))
	if err != nil {
		t.Fatal(err)
	}
	rules := map[string]bool{}
	for _, v := range s.Violations {
		rules[v.Rule] = true
	}
	for _, r := range []string{"run-as-non-root", "image-pinned", "read-only-root", "resources", "network-policy"} {
		if !rules[r] {
			t.Errorf("pod: %s not reported", r)
		}
	}
	if rules["probes"] {
		t.Error("probes required on a run-to-completion pod")
	}
}

func TestBadInput(t *testing.T) {
	if _, err := Check(strings.NewReader("")); err == nil {
		t.Error("empty input accepted")
	}
	if _, err := Check(strings.NewReader("kind: [unclosed")); err == nil {
		t.Error("broken YAML accepted")
	}
}
