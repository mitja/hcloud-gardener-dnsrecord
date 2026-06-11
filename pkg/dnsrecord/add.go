// SPDX-FileCopyrightText: 2026 Mitja Martini
//
// SPDX-License-Identifier: Apache-2.0

package dnsrecord

import (
	"context"

	"github.com/gardener/gardener/extensions/pkg/controller/dnsrecord"
	extensionsv1alpha1 "github.com/gardener/gardener/pkg/apis/extensions/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/mitja/hcloud-gardener-dnsrecord/pkg/hcloud"
)

// Type is the DNSRecord provider type this extension claims.
const Type = "hcloud-dns"

// DefaultAddOptions are the default AddOptions for AddToManager.
var DefaultAddOptions = AddOptions{}

// AddOptions are the options to apply when adding the DNSRecord controller to a
// manager.
type AddOptions struct {
	// Controller are the controller.Options.
	Controller controller.Options
	// IgnoreOperationAnnotation specifies whether to ignore the operation
	// annotation or not.
	IgnoreOperationAnnotation bool
	// ExtensionClass defines the extension class this extension is responsible for.
	ExtensionClass extensionsv1alpha1.ExtensionClass
	// Factory builds the Hetzner Cloud DNS client. If nil, the default
	// HTTP-backed factory is used.
	Factory hcloud.Factory
}

// AddToManager adds the DNSRecord controller with the default options to the
// manager.
func AddToManager(ctx context.Context, mgr manager.Manager) error {
	return AddToManagerWithOptions(ctx, mgr, DefaultAddOptions)
}

// AddToManagerWithOptions adds the DNSRecord controller with the given options
// to the manager.
func AddToManagerWithOptions(ctx context.Context, mgr manager.Manager, opts AddOptions) error {
	factory := opts.Factory
	if factory == nil {
		factory = hcloud.DefaultFactory()
	}
	return dnsrecord.Add(mgr, dnsrecord.AddArgs{
		Actuator:                  NewActuator(mgr, factory),
		ControllerOptions:         opts.Controller,
		Predicates:                dnsrecord.DefaultPredicates(ctx, mgr, opts.IgnoreOperationAnnotation),
		Type:                      Type,
		IgnoreOperationAnnotation: opts.IgnoreOperationAnnotation,
		ExtensionClass:            opts.ExtensionClass,
	})
}
