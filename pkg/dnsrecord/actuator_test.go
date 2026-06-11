// SPDX-FileCopyrightText: 2026 Mitja Martini
//
// SPDX-License-Identifier: Apache-2.0

package dnsrecord

import (
	"context"
	"errors"
	"net/http"
	"testing"

	extensionsv1alpha1 "github.com/gardener/gardener/pkg/apis/extensions/v1alpha1"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/mitja/hcloud-gardener-dnsrecord/pkg/hcloud"
)

// fakeHCloud is an in-memory implementation of hcloud.Interface.
type fakeHCloud struct {
	zones []hcloud.Zone

	created []hcloud.RRSet
	setRecs map[string][]hcloud.Record
	ttls    map[string]*int64
	deleted []string

	createErr error
	existing  map[string]bool // name|type already exists -> CreateRRSet returns conflict
	deleteErr error
}

func newFakeHCloud(zones ...hcloud.Zone) *fakeHCloud {
	return &fakeHCloud{
		zones:    zones,
		setRecs:  map[string][]hcloud.Record{},
		ttls:     map[string]*int64{},
		existing: map[string]bool{},
	}
}

func key(name, t string) string { return name + "|" + t }

func (f *fakeHCloud) ListZones(_ context.Context) ([]hcloud.Zone, error) { return f.zones, nil }

func (f *fakeHCloud) CreateRRSet(_ context.Context, _ int64, rr hcloud.RRSet) (*hcloud.RRSet, error) {
	if f.createErr != nil {
		return nil, f.createErr
	}
	if f.existing[key(rr.Name, rr.Type)] {
		return nil, &hcloud.APIError{StatusCode: http.StatusConflict, Code: "conflict"}
	}
	f.created = append(f.created, rr)
	return &rr, nil
}

func (f *fakeHCloud) GetRRSet(_ context.Context, _ int64, name, t string) (*hcloud.RRSet, error) {
	return nil, &hcloud.APIError{StatusCode: http.StatusNotFound}
}

func (f *fakeHCloud) SetRecords(_ context.Context, _ int64, name, t string, recs []hcloud.Record) error {
	f.setRecs[key(name, t)] = recs
	return nil
}

func (f *fakeHCloud) ChangeTTL(_ context.Context, _ int64, name, t string, ttl *int64) error {
	f.ttls[key(name, t)] = ttl
	return nil
}

func (f *fakeHCloud) DeleteRRSet(_ context.Context, _ int64, name, t string) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.deleted = append(f.deleted, key(name, t))
	return nil
}

func newScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	if err := extensionsv1alpha1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	return s
}

func newDNSRecord(name, recordType string, values []string, ttl *int64) *extensionsv1alpha1.DNSRecord {
	return &extensionsv1alpha1.DNSRecord{
		ObjectMeta: metav1.ObjectMeta{Name: "test-record", Namespace: "shoot--foo--bar"},
		Spec: extensionsv1alpha1.DNSRecordSpec{
			Name:       name,
			RecordType: extensionsv1alpha1.DNSRecordType(recordType),
			Values:     values,
			TTL:        ttl,
			SecretRef: corev1.SecretReference{
				Name:      "dns-secret",
				Namespace: "shoot--foo--bar",
			},
		},
	}
}

func newSecret(data map[string][]byte) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "dns-secret", Namespace: "shoot--foo--bar"},
		Data:       data,
	}
}

func newActuatorForTest(t *testing.T, fake *fakeHCloud, objs ...client.Object) (*actuator, client.Client) {
	t.Helper()
	scheme := newScheme(t)
	cl := buildFakeClient(scheme, objs...)
	return &actuator{
		client:  cl,
		factory: hcloud.FactoryFunc(func(_ string) hcloud.Interface { return fake }),
	}, cl
}

func buildFakeClient(scheme *runtime.Scheme, objs ...client.Object) client.Client {
	return fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objs...).
		WithStatusSubresource(&extensionsv1alpha1.DNSRecord{}).
		Build()
}

func TestReconcile_CreatesRRSetAndSetsStatusZone(t *testing.T) {
	fh := newFakeHCloud(hcloud.Zone{ID: 99, Name: "internal.g.paasbox.com", Status: "ok"})
	dns := newDNSRecord("api.work.internal.g.paasbox.com", "A", []string{"46.225.43.151"}, ptr.To(int64(300)))
	secret := newSecret(map[string][]byte{"hcloudToken": []byte("tok")})
	a, cl := newActuatorForTest(t, fh, dns, secret)

	if err := a.Reconcile(context.Background(), logr.Discard(), dns, nil); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if len(fh.created) != 1 {
		t.Fatalf("expected 1 created rrset, got %d", len(fh.created))
	}
	rr := fh.created[0]
	if rr.Name != "api.work" || rr.Type != "A" || rr.TTL == nil || *rr.TTL != 300 {
		t.Fatalf("unexpected rrset: %+v", rr)
	}
	if len(rr.Records) != 1 || rr.Records[0].Value != "46.225.43.151" {
		t.Fatalf("unexpected records: %+v", rr.Records)
	}

	updated := &extensionsv1alpha1.DNSRecord{}
	if err := cl.Get(context.Background(), client.ObjectKeyFromObject(dns), updated); err != nil {
		t.Fatal(err)
	}
	if updated.Status.Zone == nil || *updated.Status.Zone != "internal.g.paasbox.com" {
		t.Fatalf("expected status.zone internal.g.paasbox.com, got %v", updated.Status.Zone)
	}
}

func TestReconcile_DefaultTTL(t *testing.T) {
	fh := newFakeHCloud(hcloud.Zone{ID: 1, Name: "paasbox.com"})
	dns := newDNSRecord("api.paasbox.com", "A", []string{"1.2.3.4"}, nil)
	secret := newSecret(map[string][]byte{"hcloudToken": []byte("tok")})
	a, _ := newActuatorForTest(t, fh, dns, secret)

	if err := a.Reconcile(context.Background(), logr.Discard(), dns, nil); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if fh.created[0].TTL == nil || *fh.created[0].TTL != 120 {
		t.Fatalf("expected default TTL 120, got %v", fh.created[0].TTL)
	}
}

func TestReconcile_UpsertOnConflict(t *testing.T) {
	fh := newFakeHCloud(hcloud.Zone{ID: 1, Name: "paasbox.com"})
	fh.existing[key("api", "A")] = true
	dns := newDNSRecord("api.paasbox.com", "A", []string{"5.6.7.8"}, ptr.To(int64(60)))
	secret := newSecret(map[string][]byte{"hcloudToken": []byte("tok")})
	a, _ := newActuatorForTest(t, fh, dns, secret)

	if err := a.Reconcile(context.Background(), logr.Discard(), dns, nil); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(fh.created) != 0 {
		t.Fatalf("expected no create on conflict, got %d", len(fh.created))
	}
	recs := fh.setRecs[key("api", "A")]
	if len(recs) != 1 || recs[0].Value != "5.6.7.8" {
		t.Fatalf("expected set_records called with new value, got %+v", recs)
	}
	if ttl := fh.ttls[key("api", "A")]; ttl == nil || *ttl != 60 {
		t.Fatalf("expected change_ttl 60, got %v", ttl)
	}
}

func TestReconcile_TokenFallbackKey(t *testing.T) {
	fh := newFakeHCloud(hcloud.Zone{ID: 1, Name: "paasbox.com"})
	dns := newDNSRecord("api.paasbox.com", "A", []string{"1.1.1.1"}, nil)
	secret := newSecret(map[string][]byte{"token": []byte("fallback")})
	a, _ := newActuatorForTest(t, fh, dns, secret)

	if err := a.Reconcile(context.Background(), logr.Discard(), dns, nil); err != nil {
		t.Fatalf("Reconcile with fallback token key: %v", err)
	}
}

func TestReconcile_MissingTokenKey(t *testing.T) {
	fh := newFakeHCloud(hcloud.Zone{ID: 1, Name: "paasbox.com"})
	dns := newDNSRecord("api.paasbox.com", "A", []string{"1.1.1.1"}, nil)
	secret := newSecret(map[string][]byte{"other": []byte("x")})
	a, _ := newActuatorForTest(t, fh, dns, secret)

	if err := a.Reconcile(context.Background(), logr.Discard(), dns, nil); err == nil {
		t.Fatal("expected error for missing token key")
	}
}

func TestReconcile_NoZoneMatch(t *testing.T) {
	fh := newFakeHCloud(hcloud.Zone{ID: 1, Name: "other.com"})
	dns := newDNSRecord("api.paasbox.com", "A", []string{"1.1.1.1"}, nil)
	secret := newSecret(map[string][]byte{"hcloudToken": []byte("tok")})
	a, _ := newActuatorForTest(t, fh, dns, secret)

	if err := a.Reconcile(context.Background(), logr.Discard(), dns, nil); err == nil {
		t.Fatal("expected error when no zone matches")
	}
}

func TestReconcile_ExplicitZone(t *testing.T) {
	fh := newFakeHCloud(
		hcloud.Zone{ID: 1, Name: "paasbox.com"},
		hcloud.Zone{ID: 2, Name: "g.paasbox.com"},
	)
	dns := newDNSRecord("api.g.paasbox.com", "A", []string{"1.1.1.1"}, nil)
	dns.Spec.Zone = ptr.To("g.paasbox.com")
	secret := newSecret(map[string][]byte{"hcloudToken": []byte("tok")})
	a, _ := newActuatorForTest(t, fh, dns, secret)

	if err := a.Reconcile(context.Background(), logr.Discard(), dns, nil); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if fh.created[0].Name != "api" {
		t.Fatalf("expected relative name 'api' against explicit zone, got %q", fh.created[0].Name)
	}
}

func TestDelete_DeletesRRSet(t *testing.T) {
	fh := newFakeHCloud(hcloud.Zone{ID: 1, Name: "paasbox.com"})
	dns := newDNSRecord("api.paasbox.com", "A", []string{"1.1.1.1"}, nil)
	secret := newSecret(map[string][]byte{"hcloudToken": []byte("tok")})
	a, _ := newActuatorForTest(t, fh, dns, secret)

	if err := a.Delete(context.Background(), logr.Discard(), dns, nil); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if len(fh.deleted) != 1 || fh.deleted[0] != key("api", "A") {
		t.Fatalf("expected delete of api/A, got %+v", fh.deleted)
	}
}

func TestDelete_404IsSuccess(t *testing.T) {
	fh := newFakeHCloud(hcloud.Zone{ID: 1, Name: "paasbox.com"})
	fh.deleteErr = &hcloud.APIError{StatusCode: http.StatusNotFound}
	dns := newDNSRecord("api.paasbox.com", "A", []string{"1.1.1.1"}, nil)
	secret := newSecret(map[string][]byte{"hcloudToken": []byte("tok")})
	a, _ := newActuatorForTest(t, fh, dns, secret)

	if err := a.Delete(context.Background(), logr.Discard(), dns, nil); err != nil {
		t.Fatalf("expected 404 on delete to be success, got %v", err)
	}
}

func TestDelete_OtherErrorPropagates(t *testing.T) {
	fh := newFakeHCloud(hcloud.Zone{ID: 1, Name: "paasbox.com"})
	fh.deleteErr = &hcloud.APIError{StatusCode: http.StatusInternalServerError}
	dns := newDNSRecord("api.paasbox.com", "A", []string{"1.1.1.1"}, nil)
	secret := newSecret(map[string][]byte{"hcloudToken": []byte("tok")})
	a, _ := newActuatorForTest(t, fh, dns, secret)

	if err := a.Delete(context.Background(), logr.Discard(), dns, nil); err == nil {
		t.Fatal("expected non-404 delete error to propagate")
	}
}

func TestForceDeleteEqualsDelete(t *testing.T) {
	fh := newFakeHCloud(hcloud.Zone{ID: 1, Name: "paasbox.com"})
	dns := newDNSRecord("api.paasbox.com", "A", []string{"1.1.1.1"}, nil)
	secret := newSecret(map[string][]byte{"hcloudToken": []byte("tok")})
	a, _ := newActuatorForTest(t, fh, dns, secret)

	if err := a.ForceDelete(context.Background(), logr.Discard(), dns, nil); err != nil {
		t.Fatalf("ForceDelete: %v", err)
	}
	if len(fh.deleted) != 1 {
		t.Fatalf("expected ForceDelete to delete rrset, got %+v", fh.deleted)
	}
}

func TestMigrateIsNoOp(t *testing.T) {
	fh := newFakeHCloud()
	dns := newDNSRecord("api.paasbox.com", "A", []string{"1.1.1.1"}, nil)
	a, _ := newActuatorForTest(t, fh, dns)

	if err := a.Migrate(context.Background(), logr.Discard(), dns, nil); err != nil {
		t.Fatalf("Migrate should be a no-op, got %v", err)
	}
	if len(fh.deleted) != 0 || len(fh.created) != 0 {
		t.Fatal("Migrate must not touch the rrset")
	}
}

func TestRestoreCallsReconcile(t *testing.T) {
	fh := newFakeHCloud(hcloud.Zone{ID: 1, Name: "paasbox.com"})
	dns := newDNSRecord("api.paasbox.com", "A", []string{"1.1.1.1"}, nil)
	secret := newSecret(map[string][]byte{"hcloudToken": []byte("tok")})
	a, _ := newActuatorForTest(t, fh, dns, secret)

	if err := a.Restore(context.Background(), logr.Discard(), dns, nil); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if len(fh.created) != 1 {
		t.Fatalf("expected Restore to create rrset, got %d", len(fh.created))
	}
}

func TestReconcile_CreateErrorPropagates(t *testing.T) {
	fh := newFakeHCloud(hcloud.Zone{ID: 1, Name: "paasbox.com"})
	fh.createErr = errors.New("boom")
	dns := newDNSRecord("api.paasbox.com", "A", []string{"1.1.1.1"}, nil)
	secret := newSecret(map[string][]byte{"hcloudToken": []byte("tok")})
	a, _ := newActuatorForTest(t, fh, dns, secret)

	if err := a.Reconcile(context.Background(), logr.Discard(), dns, nil); err == nil {
		t.Fatal("expected create error to propagate")
	}
}
