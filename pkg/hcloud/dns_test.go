// SPDX-FileCopyrightText: 2026 Mitja Martini
//
// SPDX-License-Identifier: Apache-2.0

package hcloud

import (
	"reflect"
	"testing"
)

func TestFindZoneForName(t *testing.T) {
	zones := []Zone{
		{ID: 1, Name: "paasbox.com"},
		{ID: 2, Name: "internal.g.paasbox.com"},
		{ID: 3, Name: "g.paasbox.com"},
		{ID: 4, Name: "example.org"},
	}

	tests := []struct {
		name    string
		fqdn    string
		wantID  int64
		wantNil bool
	}{
		{"longest suffix wins", "api.work.internal.g.paasbox.com", 2, false},
		{"shorter suffix", "api.shoots.g.paasbox.com", 3, false},
		{"apex exact match", "paasbox.com", 1, false},
		{"apex exact match with trailing dot", "internal.g.paasbox.com.", 2, false},
		{"single label under apex", "www.paasbox.com", 1, false},
		{"case insensitive", "API.Internal.G.PaasBox.Com", 2, false},
		{"no match", "api.notmine.com", 0, true},
		{"label-boundary guard: not a suffix match", "myexample.org", 0, true},
		{"label-boundary guard: superstring", "fakepaasbox.com", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FindZoneForName(zones, tt.fqdn)
			if tt.wantNil {
				if got != nil {
					t.Fatalf("expected nil, got zone %+v", got)
				}
				return
			}
			if got == nil {
				t.Fatalf("expected zone id %d, got nil", tt.wantID)
			}
			if got.ID != tt.wantID {
				t.Fatalf("expected zone id %d, got %d (%s)", tt.wantID, got.ID, got.Name)
			}
		})
	}
}

func TestFindZoneForName_Empty(t *testing.T) {
	if got := FindZoneForName(nil, "api.example.com"); got != nil {
		t.Fatalf("expected nil for empty zone list, got %+v", got)
	}
}

func TestRelativeName(t *testing.T) {
	tests := []struct {
		name    string
		fqdn    string
		zone    string
		want    string
		wantErr bool
	}{
		{"multi-label", "api.work.internal.g.paasbox.com", "internal.g.paasbox.com", "api.work", false},
		{"single label", "api.paasbox.com", "paasbox.com", "api", false},
		{"apex", "paasbox.com", "paasbox.com", "@", false},
		{"apex with trailing dots", "paasbox.com.", "paasbox.com.", "@", false},
		{"case folding", "API.PaasBox.Com", "paasbox.com", "api", false},
		{"not within zone", "api.other.com", "paasbox.com", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := RelativeName(tt.fqdn, tt.zone)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("expected %q, got %q", tt.want, got)
			}
		})
	}
}

func TestNormalizeRecordValues(t *testing.T) {
	tests := []struct {
		name   string
		rrType string
		values []string
		want   []Record
	}{
		{
			name:   "A passthrough",
			rrType: "A",
			values: []string{"46.225.43.151", "1.2.3.4"},
			want:   []Record{{Value: "46.225.43.151"}, {Value: "1.2.3.4"}},
		},
		{
			name:   "AAAA passthrough",
			rrType: "AAAA",
			values: []string{"2001:db8::1"},
			want:   []Record{{Value: "2001:db8::1"}},
		},
		{
			name:   "CNAME gets trailing dot",
			rrType: "CNAME",
			values: []string{"target.example.com"},
			want:   []Record{{Value: "target.example.com."}},
		},
		{
			name:   "CNAME already fully qualified",
			rrType: "CNAME",
			values: []string{"target.example.com."},
			want:   []Record{{Value: "target.example.com."}},
		},
		{
			name:   "TXT gets quoted",
			rrType: "TXT",
			values: []string{"hello world"},
			want:   []Record{{Value: `"hello world"`}},
		},
		{
			name:   "TXT already quoted is left alone",
			rrType: "TXT",
			values: []string{`"v=spf1 -all"`},
			want:   []Record{{Value: `"v=spf1 -all"`}},
		},
		{
			name:   "TXT with inner quote is escaped",
			rrType: "TXT",
			values: []string{`say "hi"`},
			want:   []Record{{Value: `"say \"hi\""`}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizeRecordValues(tt.rrType, tt.values)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("expected %+v, got %+v", tt.want, got)
			}
		})
	}
}
