// Copyright 2026 Dimitri Koshkin. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package mirrord_test

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"github.com/dkoshkin/kmirror/internal/mirrord"
)

func TestConfigWrite(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		stealPorts []int32
		want       string
	}{{
		name:       "steal ports",
		stealPorts: []int32{9443},
		want: `{"target":{"path":"pod/manager-abc/container/manager","namespace":"ns"},` +
			`"feature":{"env":true,"fs":{"mode":"localwithoverrides","read_only":["^/var/run/secrets/.*"]},` +
			`"network":{"incoming":{"mode":"steal","ports":[9443]},"outgoing":true}},"telemetry":false}`,
	}, {
		name: "no steal ports",
		want: `{"target":{"path":"pod/manager-abc/container/manager","namespace":"ns"},` +
			`"feature":{"env":true,"fs":{"mode":"localwithoverrides","read_only":["^/var/run/secrets/.*"]},` +
			`"network":{"incoming":"off","outgoing":true}},"telemetry":false}`,
	}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := mirrord.NewConfig("ns", "manager-abc", "manager", tt.stealPorts, []string{"^/var/run/secrets/.*"})
			path, err := cfg.Write(t.TempDir())
			if err != nil {
				t.Fatalf("Write() error = %v", err)
			}
			data, err := os.ReadFile(path) //nolint:gosec // Reads the file the test just wrote.
			if err != nil {
				t.Fatal(err)
			}
			var compact bytes.Buffer
			if err := json.Compact(&compact, data); err != nil {
				t.Fatal(err)
			}
			if compact.String() != tt.want {
				t.Errorf("config =\n%s\nwant\n%s", compact.String(), tt.want)
			}
		})
	}
}

func TestWriteKubeconfig(t *testing.T) {
	t.Parallel()

	raw := &clientcmdapi.Config{
		CurrentContext: "kind",
		Contexts: map[string]*clientcmdapi.Context{
			"kind":  {Cluster: "kind", AuthInfo: "admin"},
			"other": {Cluster: "other", AuthInfo: "other"},
		},
		Clusters: map[string]*clientcmdapi.Cluster{
			"kind":  {Server: "https://127.0.0.1:6443", CertificateAuthorityData: []byte("ca")},
			"other": {Server: "https://other:6443"},
		},
		AuthInfos: map[string]*clientcmdapi.AuthInfo{
			"admin": {Token: "token"},
			"other": {Token: "other"},
		},
	}
	path, err := mirrord.WriteKubeconfig(raw, t.TempDir())
	if err != nil {
		t.Fatalf("WriteKubeconfig() error = %v", err)
	}
	got, err := clientcmd.LoadFromFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.CurrentContext != "kind" || len(got.Contexts) != 1 ||
		got.Clusters["kind"].Server != "https://127.0.0.1:6443" {
		t.Errorf("kubeconfig is not minified to the current context: %+v", got)
	}
	if len(raw.Contexts) != 2 {
		t.Error("WriteKubeconfig() modified its input")
	}
}
