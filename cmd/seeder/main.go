// Command seeder loads the anonymized seed into the app (Helm post-install hook) or verifies it (helm test).
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/EquinoxWN/ephemeral-test-envs/internal/seeder"
)

// main runs "load" or "verify" against the app and exits non-zero on any mismatch.
func main() {
	if len(os.Args) < 2 || (os.Args[1] != "load" && os.Args[1] != "verify") {
		fmt.Fprintln(os.Stderr, "usage: seeder load|verify -url URL -seed FILE")
		os.Exit(2)
	}
	fs := flag.NewFlagSet(os.Args[1], flag.ExitOnError)
	base := fs.String("url", "http://app-api:8080", "application base URL")
	path := fs.String("seed", "/seed/seed.json", "seed file")
	timeout := fs.Duration("timeout", 2*time.Minute, "overall timeout")
	_ = fs.Parse(os.Args[2:])
	if err := run(os.Args[1], *base, *path, *timeout); err != nil {
		fmt.Fprintln(os.Stderr, "seeder:", err)
		os.Exit(1)
	}
}

// run reads the seed and performs one action.
func run(action, base, path string, timeout time.Duration) error {
	f, err := os.Open(path) // #nosec G304 -- path is the mounted seed file given by the chart
	if err != nil {
		return err
	}
	defer f.Close()
	seed, err := seeder.ReadSeed(f)
	if err != nil {
		return fmt.Errorf("seed file: %w", err)
	}
	c, err := seeder.NewClient(base)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	act := c.Load
	if action == "verify" {
		act = c.Verify
	}
	st, err := act(ctx, seed)
	if err != nil {
		return err
	}
	fmt.Printf("%s ok: %d customers, %d orders, checksum %s\n", action, st.Customers, st.Orders, st.Checksum)
	return nil
}
