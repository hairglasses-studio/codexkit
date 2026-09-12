package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/hairglasses-studio/codexkit/pkg/sandbox"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "check":
		report, err := sandbox.CheckHealth()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error checking sandbox: %v\n", err)
			os.Exit(1)
		}
		data, _ := json.MarshalIndent(report, "", "  ")
		fmt.Println(string(data))
		if !report.Healthy {
			os.Exit(1)
		}
	case "bench":
		benchFlags := flag.NewFlagSet("bench", flag.ExitOnError)
		iters := benchFlags.Int("iterations", 10, "number of iterations")
		_ = benchFlags.Parse(os.Args[2:])
		report, err := sandbox.RunBenchmark(*iters)
		if err != nil {
			fmt.Fprintf(os.Stderr, "benchmark error: %v\n", err)
			os.Exit(1)
		}
		data, _ := json.MarshalIndent(report, "", "  ")
		fmt.Println(string(data))
		if !report.TeardownSLAOK || !report.ErrorRateSLAOK {
			os.Exit(1)
		}
	case "exec":
		execFlags := flag.NewFlagSet("exec", flag.ExitOnError)
		workDir := execFlags.String("work-dir", "", "working directory")
		enableNet := execFlags.Bool("net", false, "enable network")
		timeoutSec := execFlags.Int("timeout", 30, "timeout in seconds")
		_ = execFlags.Parse(os.Args[2:])

		args := execFlags.Args()
		if len(args) == 0 {
			fmt.Fprintf(os.Stderr, "exec requires at least one command argument\n")
			os.Exit(1)
		}

		cfg := sandbox.DefaultConfig()
		cfg.HostWorkDir = *workDir
		cfg.EphemeralWorkDir = (*workDir == "")
		cfg.UnshareNet = !*enableNet
		cfg.Timeout = time.Duration(*timeoutSec) * time.Second

		sb, err := sandbox.New(cfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "init sandbox: %v\n", err)
			os.Exit(1)
		}
		defer sb.Close()

		res, err := sb.Run(context.Background(), args...)
		if err != nil {
			fmt.Fprintf(os.Stderr, "run error: %v\n", err)
		}
		if res != nil {
			os.Stdout.WriteString(res.Stdout)
			os.Stderr.WriteString(res.Stderr)
			os.Exit(res.ExitCode)
		}
		os.Exit(1)
	case "script":
		scriptFlags := flag.NewFlagSet("script", flag.ExitOnError)
		workDir := scriptFlags.String("work-dir", "", "working directory")
		timeoutSec := scriptFlags.Int("timeout", 30, "timeout in seconds")
		_ = scriptFlags.Parse(os.Args[2:])

		args := scriptFlags.Args()
		if len(args) == 0 {
			fmt.Fprintf(os.Stderr, "script requires a script string\n")
			os.Exit(1)
		}

		cfg := sandbox.DefaultConfig()
		cfg.HostWorkDir = *workDir
		cfg.EphemeralWorkDir = (*workDir == "")
		cfg.Timeout = time.Duration(*timeoutSec) * time.Second

		sb, err := sandbox.New(cfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "init sandbox: %v\n", err)
			os.Exit(1)
		}
		defer sb.Close()

		res, err := sb.RunScript(context.Background(), args[0])
		if err != nil {
			fmt.Fprintf(os.Stderr, "run error: %v\n", err)
		}
		if res != nil {
			os.Stdout.WriteString(res.Stdout)
			os.Stderr.WriteString(res.Stderr)
			os.Exit(res.ExitCode)
		}
		os.Exit(1)
	default:
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprintf(os.Stderr, "Usage: codexkit-sandbox <command> [options]\n")
	fmt.Fprintf(os.Stderr, "Commands:\n")
	fmt.Fprintf(os.Stderr, "  check                     Verify bwrap and invariant enforcement\n")
	fmt.Fprintf(os.Stderr, "  bench [--iterations N]    Run latency and teardown benchmark\n")
	fmt.Fprintf(os.Stderr, "  exec [--net] [--work-dir DIR] COMMAND [ARGS...]\n")
	fmt.Fprintf(os.Stderr, "  script [--work-dir DIR] SCRIPT\n")
}
