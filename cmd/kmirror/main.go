// Copyright 2026 Dimitri Koshkin. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Command kmirror swaps a running in-cluster Kubernetes controller for a locally built binary using mirrord.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"k8s.io/component-base/version"

	"github.com/dkoshkin/kmirror/internal/kmirror"
)

const usage = `Usage: kmirror -n NAMESPACE -d DEPLOYMENT BINARY [-- EXTRA_ARGS...]

Runs BINARY, a locally built controller manager, in place of the leader Pod of DEPLOYMENT.
kmirror takes over the leader-election Lease, inherits the container's args (minus flags such
as --kubeconfig, leader election, metrics and health addresses), and runs BINARY under mirrord
with the Pod's environment, network and ServiceAccount. On exit it releases the Lease.

EXTRA_ARGS are appended after the inherited args, so they can override them.

On Linux, BINARY must be dynamically linked: CGO_ENABLED=1 go build -o bin/manager ./cmd/manager

Example:
  kmirror -n capk-system -d capk-controller-manager ./bin/manager -- --v=4

Options:
`

func main() {
	os.Exit(run())
}

func run() int {
	flags := flag.NewFlagSet("kmirror", flag.ContinueOnError)
	flags.Usage = func() {
		_, _ = fmt.Fprint(flags.Output(), usage)
		flags.PrintDefaults()
	}
	namespace := flags.String("n", "", "Namespace of the Deployment (required)")
	deployment := flags.String("d", "", "Controller Deployment (required)")
	showVersion := flags.Bool("version", false, "Print version information and exit")
	if err := flags.Parse(os.Args[1:]); err != nil {
		return 2
	}
	if *showVersion {
		fmt.Println(version.Get().String())
		return 0
	}

	args := flags.Args()
	if *namespace == "" || *deployment == "" || len(args) == 0 || args[0] == "--" {
		flags.Usage()
		return 2
	}
	binary, extra := args[0], args[1:]
	if len(extra) > 0 && extra[0] == "--" {
		extra = extra[1:]
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	code, err := kmirror.Run(ctx, &kmirror.Options{
		Namespace:  *namespace,
		Deployment: *deployment,
		Binary:     binary,
		ExtraArgs:  extra,
		Log:        os.Stderr,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "kmirror: error: %v\n", err)
	}
	return code
}
