// Copyright 2026 Dimitri Koshkin. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package mirrord writes the mirrord configuration used to run the local binary.
package mirrord

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// Config is the subset of the mirrord config file kmirror uses.
// See https://metalbear.com/mirrord/docs/config/options.
type Config struct {
	Kubeconfig string  `json:"kubeconfig,omitempty"`
	Target     Target  `json:"target"`
	Feature    Feature `json:"feature"`
	Telemetry  bool    `json:"telemetry"`
}

// Target is the Pod container mirrord runs against.
type Target struct {
	Path      string `json:"path"`
	Namespace string `json:"namespace"`
}

// Feature configures which parts of the remote Pod the local process uses.
type Feature struct {
	Env     bool    `json:"env"`
	FS      FS      `json:"fs"`
	Network Network `json:"network"`
}

// FS reads files locally, except for paths matching ReadOnly, which are read from the Pod.
type FS struct {
	Mode     string   `json:"mode"`
	ReadOnly []string `json:"read_only,omitempty"`
}

// Network routes outgoing traffic through the Pod and steals incoming traffic on the given ports.
type Network struct {
	Incoming any  `json:"incoming"`
	Outgoing bool `json:"outgoing"`
}

// Incoming steals traffic on the listed ports only.
type Incoming struct {
	Mode  string  `json:"mode"`
	Ports []int32 `json:"ports"`
}

// NewConfig returns the config to run against the container of a Pod.
// Only stealPorts are stolen; with no ports, incoming traffic is left to the Pod.
func NewConfig(namespace, pod, container string, stealPorts []int32, remoteReadPaths []string) *Config {
	var incoming any = "off"
	if len(stealPorts) > 0 {
		incoming = Incoming{Mode: "steal", Ports: stealPorts}
	}
	return &Config{
		Target: Target{
			Path:      fmt.Sprintf("pod/%s/container/%s", pod, container),
			Namespace: namespace,
		},
		Feature: Feature{
			Env: true,
			FS: FS{
				Mode:     "localwithoverrides",
				ReadOnly: remoteReadPaths,
			},
			Network: Network{Incoming: incoming, Outgoing: true},
		},
	}
}

// Write writes the config to dir/mirrord.json and returns its path.
func (c *Config) Write(dir string) (string, error) {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, "mirrord.json")
	return path, os.WriteFile(path, data, 0o600)
}

// WriteKubeconfig writes a flattened copy of the current context of rawConfig to dir/kubeconfig.
// mirrord uses it, while the local binary runs without KUBECONFIG so it uses the in-cluster config.
func WriteKubeconfig(rawConfig *clientcmdapi.Config, dir string) (string, error) {
	cfg := rawConfig.DeepCopy()
	if err := clientcmdapi.MinifyConfig(cfg); err != nil {
		return "", fmt.Errorf("minifying kubeconfig: %w", err)
	}
	if err := clientcmdapi.FlattenConfig(cfg); err != nil {
		return "", fmt.Errorf("flattening kubeconfig: %w", err)
	}
	path := filepath.Join(dir, "kubeconfig")
	return path, clientcmd.WriteToFile(*cfg, path)
}
