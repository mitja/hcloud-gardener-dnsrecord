// SPDX-FileCopyrightText: 2026 Mitja Martini
//
// SPDX-License-Identifier: Apache-2.0

package hcloud

import "context"

// Interface is the subset of the Hetzner Cloud DNS client used by the actuator.
// It exists so the actuator can be exercised with a fake in unit tests.
type Interface interface {
	ListZones(ctx context.Context) ([]Zone, error)
	CreateRRSet(ctx context.Context, zoneID int64, rr RRSet) (*RRSet, error)
	GetRRSet(ctx context.Context, zoneID int64, name, rrType string) (*RRSet, error)
	SetRecords(ctx context.Context, zoneID int64, name, rrType string, records []Record) error
	ChangeTTL(ctx context.Context, zoneID int64, name, rrType string, ttl *int64) error
	DeleteRRSet(ctx context.Context, zoneID int64, name, rrType string) error
}

// Factory creates an Interface from a Hetzner Cloud API token.
type Factory interface {
	NewClient(token string) Interface
}

// FactoryFunc adapts a function into a Factory.
type FactoryFunc func(token string) Interface

// NewClient implements Factory.
func (f FactoryFunc) NewClient(token string) Interface { return f(token) }

// DefaultFactory returns a Factory that builds real *Client instances.
func DefaultFactory(opts ...Option) Factory {
	return FactoryFunc(func(token string) Interface {
		return NewClient(token, opts...)
	})
}

// compile-time assertion that *Client satisfies Interface.
var _ Interface = (*Client)(nil)
