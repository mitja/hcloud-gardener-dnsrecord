// SPDX-FileCopyrightText: 2026 Mitja Martini
//
// SPDX-License-Identifier: Apache-2.0

package hcloud

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func newTestClient(t *testing.T, handler http.Handler) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return NewClient("test-token", WithBaseURL(srv.URL)), srv
}

func TestListZones_Pagination(t *testing.T) {
	var gotAuth string
	mux := http.NewServeMux()
	mux.HandleFunc("/zones", func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		w.Header().Set("Content-Type", "application/json")
		switch page {
		case 0, 1:
			next := 2
			_ = json.NewEncoder(w).Encode(zonesResponse{
				Zones: []Zone{{ID: 1, Name: "a.com", Status: "ok"}},
				Meta: struct {
					Pagination pagination `json:"pagination"`
				}{Pagination: pagination{Page: 1, NextPage: &next, LastPage: 2}},
			})
		case 2:
			_ = json.NewEncoder(w).Encode(zonesResponse{
				Zones: []Zone{{ID: 2, Name: "b.com", Status: "ok"}},
				Meta: struct {
					Pagination pagination `json:"pagination"`
				}{Pagination: pagination{Page: 2, NextPage: nil, LastPage: 2}},
			})
		default:
			t.Errorf("unexpected page %d", page)
		}
	})

	c, _ := newTestClient(t, mux)
	zones, err := c.ListZones(context.Background())
	if err != nil {
		t.Fatalf("ListZones: %v", err)
	}
	if len(zones) != 2 || zones[0].ID != 1 || zones[1].ID != 2 {
		t.Fatalf("unexpected zones: %+v", zones)
	}
	if gotAuth != "Bearer test-token" {
		t.Fatalf("unexpected auth header: %q", gotAuth)
	}
}

func TestCreateRRSet(t *testing.T) {
	var body rrsetCreateRequest
	mux := http.NewServeMux()
	mux.HandleFunc("/zones/42/rrsets", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(rrsetResponse{RRSet: RRSet{Name: body.Name, Type: body.Type, Records: body.Records}})
	})

	c, _ := newTestClient(t, mux)
	ttl := int64(300)
	rr, err := c.CreateRRSet(context.Background(), 42, RRSet{
		Name: "api.garden.g", Type: "A", TTL: &ttl, Records: []Record{{Value: "46.225.43.151"}},
	})
	if err != nil {
		t.Fatalf("CreateRRSet: %v", err)
	}
	if body.Name != "api.garden.g" || body.Type != "A" || body.TTL == nil || *body.TTL != 300 {
		t.Fatalf("unexpected request body: %+v", body)
	}
	if len(body.Records) != 1 || body.Records[0].Value != "46.225.43.151" {
		t.Fatalf("unexpected records in request: %+v", body.Records)
	}
	if rr.Name != "api.garden.g" {
		t.Fatalf("unexpected response rrset: %+v", rr)
	}
}

func TestCreateRRSet_Conflict(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/zones/42/rrsets", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":{"code":"conflict","message":"already exists"}}`))
	})

	c, _ := newTestClient(t, mux)
	_, err := c.CreateRRSet(context.Background(), 42, RRSet{Name: "api", Type: "A", Records: []Record{{Value: "1.2.3.4"}}})
	if err == nil {
		t.Fatal("expected conflict error")
	}
	if !IsConflict(err) {
		t.Fatalf("expected IsConflict, got %v", err)
	}
	if IsNotFound(err) {
		t.Fatal("conflict should not be reported as not-found")
	}
}

func TestSetRecordsAndChangeTTL(t *testing.T) {
	var setBody setRecordsRequest
	var ttlBody changeTTLRequest
	mux := http.NewServeMux()
	mux.HandleFunc("/zones/7/rrsets/api/A/actions/set_records", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&setBody)
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/zones/7/rrsets/api/A/actions/change_ttl", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&ttlBody)
		w.WriteHeader(http.StatusOK)
	})

	c, _ := newTestClient(t, mux)
	if err := c.SetRecords(context.Background(), 7, "api", "A", []Record{{Value: "9.9.9.9"}}); err != nil {
		t.Fatalf("SetRecords: %v", err)
	}
	ttl := int64(120)
	if err := c.ChangeTTL(context.Background(), 7, "api", "A", &ttl); err != nil {
		t.Fatalf("ChangeTTL: %v", err)
	}
	if len(setBody.Records) != 1 || setBody.Records[0].Value != "9.9.9.9" {
		t.Fatalf("unexpected set_records body: %+v", setBody)
	}
	if ttlBody.TTL == nil || *ttlBody.TTL != 120 {
		t.Fatalf("unexpected change_ttl body: %+v", ttlBody)
	}
}

func TestDeleteRRSet(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/zones/7/rrsets/api/A", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("expected DELETE, got %s", r.Method)
		}
		w.WriteHeader(http.StatusNoContent)
	})

	c, _ := newTestClient(t, mux)
	if err := c.DeleteRRSet(context.Background(), 7, "api", "A"); err != nil {
		t.Fatalf("DeleteRRSet: %v", err)
	}
}

func TestDeleteRRSet_404(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/zones/7/rrsets/missing/A", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"code":"not_found","message":"rrset not found"}}`))
	})

	c, _ := newTestClient(t, mux)
	err := c.DeleteRRSet(context.Background(), 7, "missing", "A")
	if err == nil {
		t.Fatal("expected not-found error")
	}
	if !IsNotFound(err) {
		t.Fatalf("expected IsNotFound, got %v", err)
	}
}

func TestGetRRSet_404(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/zones/7/rrsets/missing/A", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	c, _ := newTestClient(t, mux)
	_, err := c.GetRRSet(context.Background(), 7, "missing", "A")
	if !IsNotFound(err) {
		t.Fatalf("expected IsNotFound, got %v", err)
	}
}

func TestAPIErrorMessage(t *testing.T) {
	err := &APIError{StatusCode: 422, Code: "invalid_input", Message: "bad ttl"}
	if got := err.Error(); got == "" {
		t.Fatal("empty error string")
	}
	// sanity: ensure code appears
	if !strings.Contains(err.Error(), "invalid_input") {
		t.Fatalf("error string missing code: %s", err.Error())
	}
}

func TestEscapeRRSetName(t *testing.T) {
	for in, want := range map[string]string{
		"*.ingress.soil.g": "*.ingress.soil.g",
		"api.work.g":       "api.work.g",
		"@":                "@",
		"weird name":       "weird%20name",
	} {
		if got := escapeRRSetName(in); got != want {
			t.Errorf("escapeRRSetName(%q) = %q, want %q", in, got, want)
		}
	}
}
