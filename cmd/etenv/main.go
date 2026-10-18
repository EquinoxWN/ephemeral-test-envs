// Command etenv prepares anonymized seed data and runs a pull request's ephemeral environment.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/EquinoxWN/ephemeral-test-envs/internal/anon"
	"github.com/EquinoxWN/ephemeral-test-envs/internal/chartpolicy"
	"github.com/EquinoxWN/ephemeral-test-envs/internal/dataset"
	"github.com/EquinoxWN/ephemeral-test-envs/internal/envplan"
	"github.com/EquinoxWN/ephemeral-test-envs/internal/synth"
)

const usage = `usage: etenv <command> [flags]
  synth      write production-shaped raw data with fake personal data
  anonymize  raw data -> seed data (key from ETENV_ANON_KEY), then leak scan
  scan       check seed data for raw values and personal-data patterns
  policy     check rendered manifests (helm template output) against the safety rules
  plan       print the environment's commands as JSON
  up         create the environment, check it, tear it down, write a report
  down       delete a pull request's cluster`

// main dispatches to a subcommand; exit 1 means a check failed, 2 a usage error.
func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	cmds := map[string]func([]string) error{
		"synth": cmdSynth, "anonymize": cmdAnonymize, "scan": cmdScan, "policy": cmdPolicy,
		"plan": cmdPlan, "up": cmdUp, "down": cmdDown,
	}
	cmd, ok := cmds[os.Args[1]]
	if !ok {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	if err := cmd(os.Args[2:]); err != nil {
		fmt.Fprintln(os.Stderr, "etenv:", err)
		os.Exit(1)
	}
}

// writeJSON writes v indented to path, or stdout when path is "-".
func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if path == "-" {
		_, err = os.Stdout.Write(data)
		return err
	}
	return os.WriteFile(path, data, 0o644) // #nosec G306 -- seed and reports are not secret
}

// readJSON strictly decodes a file into v.
func readJSON(path string, v any) error {
	f, err := os.Open(path) // #nosec G304 -- operator-supplied path
	if err != nil {
		return err
	}
	defer f.Close()
	return dataset.DecodeStrict(f, v)
}

// cmdSynth writes fake raw data.
func cmdSynth(args []string) error {
	fs := flag.NewFlagSet("synth", flag.ExitOnError)
	n := fs.Int("customers", 250, "number of customers")
	seed := fs.Uint64("seed", 1, "random seed")
	out := fs.String("out", "-", "output file")
	_ = fs.Parse(args)
	raw, err := synth.Raw(*n, *seed)
	if err != nil {
		return err
	}
	return writeJSON(*out, raw)
}

// cmdAnonymize converts raw data to seed data and refuses to write it if the scan finds a leak.
func cmdAnonymize(args []string) error {
	fs := flag.NewFlagSet("anonymize", flag.ExitOnError)
	in := fs.String("in", "", "raw data file")
	out := fs.String("out", "-", "seed output file")
	k := fs.Int("k", 5, "smallest allowed group sharing country, postal prefix and birth decade")
	stats := fs.String("stats", "", "optional stats output file")
	_ = fs.Parse(args)
	key := os.Getenv("ETENV_ANON_KEY")
	if key == "" {
		return errors.New("ETENV_ANON_KEY is not set (use a secret of at least 32 bytes)")
	}
	var raw dataset.Raw
	if err := readJSON(*in, &raw); err != nil {
		return fmt.Errorf("raw data: %w", err)
	}
	a, err := anon.New([]byte(key), *k)
	if err != nil {
		return err
	}
	seed, st, err := a.Anonymize(raw)
	if err != nil {
		return err
	}
	data, err := json.Marshal(seed)
	if err != nil {
		return err
	}
	if found := anon.Scan(raw, data); len(found) > 0 {
		return fmt.Errorf("leak scan found %d problems, first: %s", len(found), found[0].Detail)
	}
	if *stats != "" {
		if err := writeJSON(*stats, st); err != nil {
			return err
		}
	}
	fmt.Fprintf(os.Stderr, "anonymized %d of %d customers and %d of %d orders (%d customers suppressed); smallest group %d (k=%d); leak scan clean\n",
		len(seed.Customers), st.Customers, len(seed.Orders), st.Orders, st.SuppressedRecords, st.MinGroupSize, *k)
	return writeJSON(*out, seed)
}

// cmdScan checks a seed file against its raw source.
func cmdScan(args []string) error {
	fs := flag.NewFlagSet("scan", flag.ExitOnError)
	rawPath := fs.String("raw", "", "raw data file")
	seedPath := fs.String("seed", "", "seed file")
	_ = fs.Parse(args)
	var raw dataset.Raw
	if err := readJSON(*rawPath, &raw); err != nil {
		return fmt.Errorf("raw data: %w", err)
	}
	data, err := os.ReadFile(*seedPath) // #nosec G304 -- operator-supplied path
	if err != nil {
		return err
	}
	var seed dataset.Seed
	if err := readJSON(*seedPath, &seed); err != nil {
		return fmt.Errorf("seed: %w", err)
	}
	if err := seed.Validate(); err != nil {
		return fmt.Errorf("seed: %w", err)
	}
	found := anon.Scan(raw, data)
	for _, f := range found {
		fmt.Printf("LEAK %s: %s\n", f.Rule, f.Detail)
	}
	if len(found) > 0 {
		return fmt.Errorf("%d leaks", len(found))
	}
	fmt.Printf("scan clean: %d customers, %d orders, smallest group %d, checksum %s\n",
		len(seed.Customers), len(seed.Orders), anon.MinGroupSize(seed.Customers), seed.Checksum())
	return nil
}

// cmdPolicy checks rendered manifests from a file or stdin.
func cmdPolicy(args []string) error {
	fs := flag.NewFlagSet("policy", flag.ExitOnError)
	in := fs.String("in", "-", "rendered manifests (helm template output)")
	_ = fs.Parse(args)
	var r io.Reader = os.Stdin
	if *in != "-" {
		f, err := os.Open(*in) // #nosec G304 -- operator-supplied path
		if err != nil {
			return err
		}
		defer f.Close()
		r = f
	}
	s, err := chartpolicy.Check(r)
	if err != nil {
		return err
	}
	for _, v := range s.Violations {
		fmt.Println("VIOLATION", v)
	}
	if len(s.Violations) > 0 {
		return fmt.Errorf("%d policy violations", len(s.Violations))
	}
	fmt.Printf("policy ok: %d objects %v, %d containers, 0 violations\n", s.Objects, s.Kinds, s.Containers)
	return nil
}

// planFlags parses the flags shared by plan, up and down.
func planFlags(name string, args []string) (c envplan.Config, report *string, annotate *bool) {
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	fs.IntVar(&c.PR, "pr", 0, "pull request number")
	fs.StringVar(&c.Commit, "commit", "", "commit SHA (7 to 40 hex characters)")
	fs.StringVar(&c.Root, "root", ".", "repository root")
	fs.StringVar(&c.NodeImage, "node-image", envplan.DefaultNodeImage, "kind node image, pinned by digest")
	fs.DurationVar(&c.Wait, "wait", 4*time.Minute, "wait limit for cluster and release")
	report = fs.String("report", "", "report output file")
	annotate = fs.Bool("annotate", false, "print a GitHub Actions annotation with the result")
	_ = fs.Parse(args)
	if root, err := filepath.Abs(c.Root); err == nil {
		c.Root = root
	}
	c.StateDir = filepath.Join(c.Root, ".tmp", "envs")
	return c, report, annotate
}

// cmdPlan prints the plan without running anything.
func cmdPlan(args []string) error {
	c, _, _ := planFlags("plan", args)
	p, err := envplan.Build(c)
	if err != nil {
		return err
	}
	return writeJSON("-", p)
}

// cmdUp runs the whole lifecycle and fails when any step or the teardown fails.
func cmdUp(args []string) error {
	c, report, annotate := planFlags("up", args)
	p, err := envplan.Build(c)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(c.StateDir, 0o750); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	r := envplan.Run(ctx, p, envplan.ExecRunner{}, os.Stdout, time.Now)
	if *report != "" {
		if err := writeJSON(*report, r); err != nil {
			return err
		}
	}
	if *annotate {
		fmt.Println(r.Annotation())
	}
	if r.Result != "pass" || !r.TornDown {
		return fmt.Errorf("environment %s: result %s, failed step %q, torn down %t", r.Cluster, r.Result, r.FailedStep, r.TornDown)
	}
	fmt.Printf("RESULT %s pass: up %.1f s, total %.1f s, torn down\n", r.Cluster, r.UpSeconds, r.TotalSeconds)
	return nil
}

// cmdDown deletes a pull request's cluster (for runs interrupted before teardown).
func cmdDown(args []string) error {
	c, _, _ := planFlags("down", args)
	if c.Commit == "" {
		c.Commit = "0000000"
	}
	p, err := envplan.Build(c)
	if err != nil {
		return err
	}
	for _, s := range p.Teardown {
		if err := (envplan.ExecRunner{}).Run(context.Background(), s, p.Env, os.Stdout); err != nil {
			return err
		}
	}
	return nil
}
