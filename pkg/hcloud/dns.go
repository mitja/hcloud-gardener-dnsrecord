// SPDX-FileCopyrightText: 2026 Mitja Martini
//
// SPDX-License-Identifier: Apache-2.0

package hcloud

import (
	"fmt"
	"strings"
)

// FindZoneForName returns the zone whose name is the longest suffix of the
// given FQDN. The match must align on a label boundary: zone "example.com"
// matches "api.example.com" and "example.com" (apex) but not "myexample.com".
// Returns nil if no zone matches.
func FindZoneForName(zones []Zone, fqdn string) *Zone {
	name := normalizeFQDN(fqdn)

	var best *Zone
	for i := range zones {
		z := &zones[i]
		zoneName := normalizeFQDN(z.Name)
		if zoneName == "" {
			continue
		}
		if name == zoneName || strings.HasSuffix(name, "."+zoneName) {
			if best == nil || len(zoneName) > len(best.Name) {
				best = z
			}
		}
	}
	return best
}

// RelativeName computes the rrset name relative to the zone. The Hetzner Cloud
// API expects names that do not end with a dot or the zone name; the zone apex
// is represented as "@".
//
// Examples (zone "example.com"):
//
//	api.work.internal.example.com -> api.work.internal
//	example.com                   -> @
func RelativeName(fqdn, zoneName string) (string, error) {
	name := normalizeFQDN(fqdn)
	zone := normalizeFQDN(zoneName)

	if name == zone {
		return "@", nil
	}
	suffix := "." + zone
	if !strings.HasSuffix(name, suffix) {
		return "", fmt.Errorf("name %q is not within zone %q", fqdn, zoneName)
	}
	rel := strings.TrimSuffix(name, suffix)
	if rel == "" {
		return "@", nil
	}
	return rel, nil
}

// normalizeFQDN lower-cases and strips a single trailing dot.
func normalizeFQDN(s string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(s)), ".")
}

// NormalizeRecordValues normalizes Gardener-provided record values into the
// form the Hetzner Cloud DNS API expects, per record type:
//
//   - A / AAAA: passed through unchanged.
//   - CNAME: the target is made a fully-qualified name with a trailing dot
//     (the API/PowerDNS backend stores CNAME/NS/SOA targets with a trailing
//     dot, as confirmed against live zone data).
//   - TXT: the value is wrapped in double quotes if not already quoted; inner
//     quotes and backslashes are escaped. Long strings are not auto-split.
func NormalizeRecordValues(rrType string, values []string) []Record {
	records := make([]Record, 0, len(values))
	for _, v := range values {
		records = append(records, Record{Value: normalizeRecordValue(rrType, v)})
	}
	return records
}

func normalizeRecordValue(rrType, value string) string {
	switch strings.ToUpper(rrType) {
	case "CNAME":
		v := strings.TrimSpace(value)
		if v == "" || v == "@" {
			return v
		}
		if !strings.HasSuffix(v, ".") {
			v += "."
		}
		return v
	case "TXT":
		return quoteTXT(value)
	default:
		return strings.TrimSpace(value)
	}
}

// quoteTXT wraps a TXT value in double quotes. Values that are already a single
// fully-quoted string are returned unchanged.
func quoteTXT(value string) string {
	if len(value) >= 2 && strings.HasPrefix(value, `"`) && strings.HasSuffix(value, `"`) {
		// Already quoted; assume the caller knows what they are doing.
		return value
	}
	escaped := strings.ReplaceAll(value, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `"`, `\"`)
	return `"` + escaped + `"`
}
