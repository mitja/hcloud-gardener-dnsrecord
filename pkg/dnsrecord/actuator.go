// SPDX-FileCopyrightText: 2026 Mitja Martini
//
// SPDX-License-Identifier: Apache-2.0

// Package dnsrecord implements the Gardener DNSRecord Actuator for the
// "hcloud-dns" provider type, backed by the project-scoped Hetzner Cloud DNS
// API.
package dnsrecord

import (
	"context"
	"fmt"

	extensionscontroller "github.com/gardener/gardener/extensions/pkg/controller"
	"github.com/gardener/gardener/extensions/pkg/controller/dnsrecord"
	gardencorev1beta1 "github.com/gardener/gardener/pkg/apis/core/v1beta1"
	gardencorev1beta1helper "github.com/gardener/gardener/pkg/apis/core/v1beta1/helper"
	extensionsv1alpha1 "github.com/gardener/gardener/pkg/apis/extensions/v1alpha1"
	extensionsv1alpha1helper "github.com/gardener/gardener/pkg/apis/extensions/v1alpha1/helper"
	kutil "github.com/gardener/gardener/pkg/utils/kubernetes"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/mitja/hcloud-gardener-dnsrecord/pkg/hcloud"
)

const (
	// secretKeyHCloudToken is the primary Secret data key for the API token.
	// It matches the key used by the provider-hcloud extension, so one Secret
	// can serve both extensions.
	secretKeyHCloudToken = "hcloudToken"
	// secretKeyTokenFallback is accepted as a fallback key.
	secretKeyTokenFallback = "token"
)

type actuator struct {
	client  client.Client
	factory hcloud.Factory
}

// NewActuator creates a new DNSRecord Actuator backed by the Hetzner Cloud DNS
// API.
func NewActuator(mgr manager.Manager, factory hcloud.Factory) dnsrecord.Actuator {
	return &actuator{
		client:  mgr.GetClient(),
		factory: factory,
	}
}

// Reconcile creates or updates (idempotent upsert) the rrset for the DNSRecord.
func (a *actuator) Reconcile(ctx context.Context, log logr.Logger, dns *extensionsv1alpha1.DNSRecord, _ *extensionscontroller.Cluster) error {
	hc, err := a.clientFromSecret(ctx, dns)
	if err != nil {
		return err
	}

	zone, err := a.getZone(ctx, log, dns, hc)
	if err != nil {
		return err
	}

	relName, err := hcloud.RelativeName(dns.Spec.Name, zone.Name)
	if err != nil {
		return gardencorev1beta1helper.NewErrorWithCodes(err, gardencorev1beta1.ErrorConfigurationProblem)
	}

	ttl := extensionsv1alpha1helper.GetDNSRecordTTL(dns.Spec.TTL)
	rrType := string(dns.Spec.RecordType)
	records := hcloud.NormalizeRecordValues(rrType, dns.Spec.Values)

	log.Info("Creating or updating DNS rrset",
		"zone", zone.Name, "zoneID", zone.ID, "name", relName, "type", rrType, "ttl", ttl, "values", dns.Spec.Values)

	if err := a.upsertRRSet(ctx, hc, zone.ID, relName, rrType, ttl, records); err != nil {
		return fmt.Errorf("could not create or update rrset %s/%s in zone %s: %w", relName, rrType, zone.Name, err)
	}

	return a.updateStatusZone(ctx, dns, zone)
}

// upsertRRSet creates the rrset, or — if it already exists — replaces its
// records and TTL.
func (a *actuator) upsertRRSet(ctx context.Context, hc hcloud.Interface, zoneID int64, name, rrType string, ttl int64, records []hcloud.Record) error {
	ttlPtr := ttl
	_, err := hc.CreateRRSet(ctx, zoneID, hcloud.RRSet{
		Name:    name,
		Type:    rrType,
		TTL:     &ttlPtr,
		Records: records,
	})
	if err == nil {
		return nil
	}
	if !hcloud.IsConflict(err) {
		return err
	}

	// Already exists: replace records and TTL.
	if err := hc.SetRecords(ctx, zoneID, name, rrType, records); err != nil {
		return fmt.Errorf("setting records: %w", err)
	}
	if err := hc.ChangeTTL(ctx, zoneID, name, rrType, &ttlPtr); err != nil {
		return fmt.Errorf("changing ttl: %w", err)
	}
	return nil
}

// Delete deletes the rrset, treating a 404 as success.
func (a *actuator) Delete(ctx context.Context, log logr.Logger, dns *extensionsv1alpha1.DNSRecord, _ *extensionscontroller.Cluster) error {
	hc, err := a.clientFromSecret(ctx, dns)
	if err != nil {
		return err
	}

	zone, err := a.getZone(ctx, log, dns, hc)
	if err != nil {
		return err
	}

	relName, err := hcloud.RelativeName(dns.Spec.Name, zone.Name)
	if err != nil {
		return gardencorev1beta1helper.NewErrorWithCodes(err, gardencorev1beta1.ErrorConfigurationProblem)
	}

	rrType := string(dns.Spec.RecordType)
	log.Info("Deleting DNS rrset", "zone", zone.Name, "zoneID", zone.ID, "name", relName, "type", rrType)

	if err := hc.DeleteRRSet(ctx, zone.ID, relName, rrType); err != nil && !hcloud.IsNotFound(err) {
		return fmt.Errorf("could not delete rrset %s/%s in zone %s: %w", relName, rrType, zone.Name, err)
	}
	return nil
}

// ForceDelete deletes the DNSRecord (same as Delete).
func (a *actuator) ForceDelete(ctx context.Context, log logr.Logger, dns *extensionsv1alpha1.DNSRecord, cluster *extensionscontroller.Cluster) error {
	return a.Delete(ctx, log, dns, cluster)
}

// Restore restores the DNSRecord (same as Reconcile).
func (a *actuator) Restore(ctx context.Context, log logr.Logger, dns *extensionsv1alpha1.DNSRecord, cluster *extensionscontroller.Cluster) error {
	return a.Reconcile(ctx, log, dns, cluster)
}

// Migrate is a no-op: the rrset must not be touched on control plane migration.
func (a *actuator) Migrate(_ context.Context, _ logr.Logger, _ *extensionsv1alpha1.DNSRecord, _ *extensionscontroller.Cluster) error {
	return nil
}

// getZone resolves the zone for the DNSRecord. It prefers spec.Zone, then
// status.Zone (cached), and otherwise resolves by longest-suffix match of
// spec.Name against the project's zones.
func (a *actuator) getZone(ctx context.Context, log logr.Logger, dns *extensionsv1alpha1.DNSRecord, hc hcloud.Interface) (*hcloud.Zone, error) {
	zones, err := hc.ListZones(ctx)
	if err != nil {
		return nil, fmt.Errorf("could not list DNS zones: %w", err)
	}

	// spec.Zone / status.Zone hold the zone name (the value we publish to
	// status.Zone below). Resolve it to the full Zone (we need the numeric ID).
	if name := zoneNameHint(dns); name != "" {
		for i := range zones {
			if zones[i].Name == name {
				return &zones[i], nil
			}
		}
		return nil, gardencorev1beta1helper.NewErrorWithCodes(
			fmt.Errorf("configured zone %q not found in project", name), gardencorev1beta1.ErrorConfigurationProblem)
	}

	zone := hcloud.FindZoneForName(zones, dns.Spec.Name)
	if zone == nil {
		return nil, gardencorev1beta1helper.NewErrorWithCodes(
			fmt.Errorf("could not find DNS zone for name %q", dns.Spec.Name), gardencorev1beta1.ErrorConfigurationProblem)
	}
	log.Info("Resolved DNS zone by longest-suffix match", "name", dns.Spec.Name, "zone", zone.Name)
	return zone, nil
}

func zoneNameHint(dns *extensionsv1alpha1.DNSRecord) string {
	if dns.Spec.Zone != nil && *dns.Spec.Zone != "" {
		return *dns.Spec.Zone
	}
	if dns.Status.Zone != nil && *dns.Status.Zone != "" {
		return *dns.Status.Zone
	}
	return ""
}

func (a *actuator) updateStatusZone(ctx context.Context, dns *extensionsv1alpha1.DNSRecord, zone *hcloud.Zone) error {
	patch := client.MergeFrom(dns.DeepCopy())
	zoneName := zone.Name
	dns.Status.Zone = &zoneName
	return a.client.Status().Patch(ctx, dns, patch)
}

// clientFromSecret builds a Hetzner Cloud DNS client from the token in the
// DNSRecord's referenced Secret.
func (a *actuator) clientFromSecret(ctx context.Context, dns *extensionsv1alpha1.DNSRecord) (hcloud.Interface, error) {
	secret, err := kutil.GetSecretByReference(ctx, a.client, &dns.Spec.SecretRef)
	if err != nil {
		return nil, fmt.Errorf("could not get secret %s/%s: %w", dns.Spec.SecretRef.Namespace, dns.Spec.SecretRef.Name, err)
	}
	token, err := tokenFromSecret(secret)
	if err != nil {
		return nil, gardencorev1beta1helper.NewErrorWithCodes(err, gardencorev1beta1.ErrorConfigurationProblem)
	}
	return a.factory.NewClient(token), nil
}

func tokenFromSecret(secret *corev1.Secret) (string, error) {
	for _, key := range []string{secretKeyHCloudToken, secretKeyTokenFallback} {
		if v, ok := secret.Data[key]; ok && len(v) > 0 {
			return string(v), nil
		}
	}
	return "", fmt.Errorf("secret %s/%s has no %q (or fallback %q) data key",
		secret.Namespace, secret.Name, secretKeyHCloudToken, secretKeyTokenFallback)
}
