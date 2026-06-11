// SPDX-FileCopyrightText: 2026 Mitja Martini
//
// SPDX-License-Identifier: Apache-2.0

// Package hcloud provides a small hand-rolled client for the project-scoped
// Hetzner Cloud DNS API (https://api.hetzner.cloud/v1). It deliberately does
// not use the legacy dns.hetzner.com API: in the Cloud API a single
// HCLOUD_TOKEN covers both servers and DNS zones/rrsets.
package hcloud

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DefaultBaseURL is the base URL of the Hetzner Cloud API.
const DefaultBaseURL = "https://api.hetzner.cloud/v1"

// defaultPerPage is the page size used when listing paginated resources.
const defaultPerPage = 50

// Client is a minimal client for the Hetzner Cloud DNS API.
type Client struct {
	token      string
	baseURL    string
	httpClient *http.Client
}

// Option configures a Client.
type Option func(*Client)

// WithBaseURL overrides the API base URL (useful for tests).
func WithBaseURL(baseURL string) Option {
	return func(c *Client) { c.baseURL = strings.TrimRight(baseURL, "/") }
}

// WithHTTPClient overrides the underlying *http.Client.
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) { c.httpClient = hc }
}

// NewClient creates a new Hetzner Cloud DNS API client. The token is sent as a
// Bearer token on every request.
func NewClient(token string, opts ...Option) *Client {
	c := &Client{
		token:      token,
		baseURL:    DefaultBaseURL,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// Zone is a DNS zone as returned by GET /zones.
type Zone struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

// Record is a single record value within an RRSet.
type Record struct {
	Value   string `json:"value"`
	Comment string `json:"comment,omitempty"`
}

// RRSet is a resource record set as returned by the API.
type RRSet struct {
	ID      string   `json:"id,omitempty"`
	Name    string   `json:"name"`
	Type    string   `json:"type"`
	TTL     *int64   `json:"ttl,omitempty"`
	Records []Record `json:"records"`
}

// pagination mirrors meta.pagination in list responses.
type pagination struct {
	Page     int  `json:"page"`
	PerPage  int  `json:"per_page"`
	NextPage *int `json:"next_page"`
	LastPage int  `json:"last_page"`
}

type zonesResponse struct {
	Zones []Zone `json:"zones"`
	Meta  struct {
		Pagination pagination `json:"pagination"`
	} `json:"meta"`
}

type rrsetResponse struct {
	RRSet RRSet `json:"rrset"`
}

type rrsetCreateRequest struct {
	Name    string   `json:"name"`
	Type    string   `json:"type"`
	TTL     *int64   `json:"ttl,omitempty"`
	Records []Record `json:"records"`
}

type setRecordsRequest struct {
	Records []Record `json:"records"`
}

type changeTTLRequest struct {
	TTL *int64 `json:"ttl"`
}

// APIError is returned for non-2xx responses and carries the HTTP status and
// the API error code (when present).
type APIError struct {
	StatusCode int
	Code       string
	Message    string
}

func (e *APIError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("hetzner cloud API error (status %d, code %q): %s", e.StatusCode, e.Code, e.Message)
	}
	return fmt.Sprintf("hetzner cloud API error (status %d): %s", e.StatusCode, e.Message)
}

// IsNotFound reports whether err is an APIError with a 404 status.
func IsNotFound(err error) bool {
	var apiErr *APIError
	if asAPIError(err, &apiErr) {
		return apiErr.StatusCode == http.StatusNotFound
	}
	return false
}

// IsConflict reports whether err indicates the rrset already exists. The
// Hetzner Cloud API returns 409 Conflict (error code "conflict" /
// "uniqueness_error") when creating an rrset whose name+type already exist.
func IsConflict(err error) bool {
	var apiErr *APIError
	if asAPIError(err, &apiErr) {
		return apiErr.StatusCode == http.StatusConflict
	}
	return false
}

func asAPIError(err error, target **APIError) bool {
	for err != nil {
		if e, ok := err.(*APIError); ok {
			*target = e
			return true
		}
		type unwrapper interface{ Unwrap() error }
		u, ok := err.(unwrapper)
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

// ListZones returns all zones in the project, following pagination.
func (c *Client) ListZones(ctx context.Context) ([]Zone, error) {
	var all []Zone
	page := 1
	for {
		q := url.Values{}
		q.Set("page", strconv.Itoa(page))
		q.Set("per_page", strconv.Itoa(defaultPerPage))

		var resp zonesResponse
		if err := c.do(ctx, http.MethodGet, "/zones?"+q.Encode(), nil, &resp); err != nil {
			return nil, err
		}
		all = append(all, resp.Zones...)

		if resp.Meta.Pagination.NextPage == nil || *resp.Meta.Pagination.NextPage == 0 {
			break
		}
		page = *resp.Meta.Pagination.NextPage
	}
	return all, nil
}

// CreateRRSet creates a new rrset in the given zone.
func (c *Client) CreateRRSet(ctx context.Context, zoneID int64, rr RRSet) (*RRSet, error) {
	body := rrsetCreateRequest{Name: rr.Name, Type: rr.Type, TTL: rr.TTL, Records: rr.Records}
	var resp rrsetResponse
	path := fmt.Sprintf("/zones/%d/rrsets", zoneID)
	if err := c.do(ctx, http.MethodPost, path, body, &resp); err != nil {
		return nil, err
	}
	return &resp.RRSet, nil
}

// GetRRSet fetches a single rrset by relative name and type. Returns an
// APIError with status 404 if it does not exist.
func (c *Client) GetRRSet(ctx context.Context, zoneID int64, name, rrType string) (*RRSet, error) {
	path := fmt.Sprintf("/zones/%d/rrsets/%s/%s", zoneID, url.PathEscape(name), url.PathEscape(rrType))
	var resp rrsetResponse
	if err := c.do(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return nil, err
	}
	return &resp.RRSet, nil
}

// SetRecords replaces all records of an existing rrset (POST .../actions/set_records).
func (c *Client) SetRecords(ctx context.Context, zoneID int64, name, rrType string, records []Record) error {
	path := fmt.Sprintf("/zones/%d/rrsets/%s/%s/actions/set_records", zoneID, url.PathEscape(name), url.PathEscape(rrType))
	return c.do(ctx, http.MethodPost, path, setRecordsRequest{Records: records}, nil)
}

// ChangeTTL updates the TTL of an existing rrset (POST .../actions/change_ttl).
func (c *Client) ChangeTTL(ctx context.Context, zoneID int64, name, rrType string, ttl *int64) error {
	path := fmt.Sprintf("/zones/%d/rrsets/%s/%s/actions/change_ttl", zoneID, url.PathEscape(name), url.PathEscape(rrType))
	return c.do(ctx, http.MethodPost, path, changeTTLRequest{TTL: ttl}, nil)
}

// DeleteRRSet deletes an rrset. A 404 from the API is surfaced as an APIError
// (callers may treat it as success via IsNotFound).
func (c *Client) DeleteRRSet(ctx context.Context, zoneID int64, name, rrType string) error {
	path := fmt.Sprintf("/zones/%d/rrsets/%s/%s", zoneID, url.PathEscape(name), url.PathEscape(rrType))
	return c.do(ctx, http.MethodDelete, path, nil, nil)
}

func (c *Client) do(ctx context.Context, method, path string, reqBody, out any) error {
	var bodyReader io.Reader
	if reqBody != nil {
		b, err := json.Marshal(reqBody)
		if err != nil {
			return fmt.Errorf("marshalling request body: %w", err)
		}
		bodyReader = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bodyReader)
	if err != nil {
		return fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("executing %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return parseAPIError(resp.StatusCode, respBody)
	}

	if out != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, out); err != nil {
			return fmt.Errorf("decoding response: %w", err)
		}
	}
	return nil
}

func parseAPIError(status int, body []byte) *APIError {
	apiErr := &APIError{StatusCode: status, Message: strings.TrimSpace(string(body))}
	var wrapper struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &wrapper); err == nil && wrapper.Error.Code != "" {
		apiErr.Code = wrapper.Error.Code
		apiErr.Message = wrapper.Error.Message
	}
	return apiErr
}
