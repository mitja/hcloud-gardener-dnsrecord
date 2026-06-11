// SPDX-FileCopyrightText: 2026 Mitja Martini
//
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"os"

	runtimelog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/mitja/hcloud-gardener-dnsrecord/cmd/gardener-extension-dnsrecord-hcloud/app"
)

// version is set via -ldflags at build time.
var version = "dev"

func main() {
	_ = version
	runtimelog.SetLogger(zap.New(zap.UseDevMode(false)))

	if err := app.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "error running %s: %v\n", app.ExtensionName, err)
		os.Exit(1)
	}
}
