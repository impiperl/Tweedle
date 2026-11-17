package api

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeCert(t *testing.T, dir, cn string) (string, string) {
	t.Helper()
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: cn},
		NotBefore: time.Now(), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &k.PublicKey, k)
	if err != nil {
		t.Fatal(err)
	}
	kb, _ := x509.MarshalECPrivateKey(k)
	cf, kf := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	os.WriteFile(kf, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}), 0o600)
	os.WriteFile(cf, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600)
	return cf, kf
}

func commonName(t *testing.T, r *certReloader) string {
	t.Helper()
	c, err := r.get(nil)
	if err != nil {
		t.Fatal(err)
	}
	x, err := x509.ParseCertificate(c.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	return x.Subject.CommonName
}

func TestCertReloaderPicksUpRenewal(t *testing.T) {
	dir := t.TempDir()
	cf, kf := writeCert(t, dir, "old")
	r, err := newCertReloader(cf, kf)
	if err != nil {
		t.Fatal(err)
	}
	if got := commonName(t, r); got != "old" {
		t.Fatalf("got %q", got)
	}
	writeCert(t, dir, "new")
	later := time.Now().Add(time.Minute)
	os.Chtimes(cf, later, later)
	if got := commonName(t, r); got != "old" {
		t.Fatalf("checked again within a minute: %q", got)
	}
	r.checked = time.Time{}
	if got := commonName(t, r); got != "new" {
		t.Fatalf("renewal not picked up: %q", got)
	}
}

func TestCertReloaderBadFiles(t *testing.T) {
	if _, err := newCertReloader("/nonexistent.pem", "/nonexistent.key"); err == nil {
		t.Fatal("expected an error")
	}
}
