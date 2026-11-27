// Copyright 2026 Dimitri Koshkin. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Command kmirror swaps a running in-cluster Kubernetes controller for a locally built binary using mirrord.
package main

import (
	"flag"
	"fmt"
	"os"

	"k8s.io/component-base/version"
)

func main() {
	showVersion := flag.Bool("version", false, "Print version information and exit.")
	flag.Parse()

	if *showVersion {
		fmt.Println(version.Get().String())
		return
	}

	fmt.Fprintln(os.Stderr, "kmirror: not implemented yet")
	os.Exit(1)
}
