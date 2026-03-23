package main

import (
	"flag"
	"fmt"
	"time"

	"github.com/meganerd/electrictown/internal/loop"
)

// cmdLoop implements the "et loop" subcommand: Ralph Wiggum unattended task runner.
func cmdLoop(args []string) error {
	fs := flag.NewFlagSet("loop", flag.ExitOnError)
	configPath := fs.String("config", "", "path to loop-specific et config YAML")
	epic := fs.String("epic", "", "scope to tickets in this epic (e.g., ET-225)")
	maxIter := fs.Int("max-iterations", 20, "maximum number of loop iterations")
	maxRetries := fs.Int("max-retries", 3, "maximum retries per ticket before skipping")
	cooldown := fs.Int("cooldown", 5, "seconds to wait between iterations")
	dryRun := fs.Bool("dry-run", false, "show what would run without executing")
	doNotify := fs.Bool("notify", true, "send voice notifications on loop events")
	template := fs.String("template", "", "path to custom prompt template file")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg := loop.Config{
		ETConfig:      *configPath,
		Epic:          *epic,
		MaxIterations: *maxIter,
		MaxRetries:    *maxRetries,
		Cooldown:      time.Duration(*cooldown) * time.Second,
		DryRun:        *dryRun,
		Notify:        *doNotify,
		Template:      *template,
	}

	runner := loop.NewRunner(cfg)
	result, err := runner.Run()
	if err != nil {
		return fmt.Errorf("loop failed: %w", err)
	}

	// Print summary report.
	fmt.Println()
	fmt.Print(loop.FormatReport(result))

	return nil
}
