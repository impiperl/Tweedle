package collect

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"os"
	"time"
)

type CertInfo struct {
	NotAfter time.Time `json:"notAfter"`
	Subject  string    `json:"subject"`
	Issuer   string    `json:"issuer"`
	// VerifyErr is set when the certificate could be read but does not
	// verify (unknown authority, wrong host name, expired …).
	VerifyErr string `json:"verifyError,omitempty"`
}

// CertFromHost connects to addr (host:port) with TLS and returns the leaf
// certificate the server presents. verify=false skips chain and host name
// verification (e.g. for mkcert certificates).
func CertFromHost(addr string, verify bool, timeout time.Duration) (*CertInfo, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host, addr = addr, net.JoinHostPort(addr, "443") // port defaults to 443
	}
	dialer := &net.Dialer{Timeout: timeout}
	// Read the certificate without verification first, so an expired or
	// otherwise broken certificate still yields its dates.
	conn, err := tls.DialWithDialer(dialer, "tcp", addr, &tls.Config{ServerName: host, InsecureSkipVerify: true})
	if err != nil {
		return nil, err
	}
	state := conn.ConnectionState()
	conn.Close()
	if len(state.PeerCertificates) == 0 {
		return nil, errors.New("no certificate")
	}
	leaf := state.PeerCertificates[0]
	info := certInfo(leaf)
	if verify {
		inter := x509.NewCertPool()
		for _, c := range state.PeerCertificates[1:] {
			inter.AddCert(c)
		}
		if _, err := leaf.Verify(x509.VerifyOptions{DNSName: host, Intermediates: inter}); err != nil {
			info.VerifyErr = err.Error()
		}
	}
	return info, nil
}

// CertFromFile reads the first certificate of a PEM file (e.g. fullchain.pem).
func CertFromFile(path string) (*CertInfo, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	for {
		var block *pem.Block
		block, data = pem.Decode(data)
		if block == nil {
			return nil, fmt.Errorf("%s: no certificate found", path)
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		c, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, err
		}
		return certInfo(c), nil
	}
}

func certInfo(c *x509.Certificate) *CertInfo {
	return &CertInfo{NotAfter: c.NotAfter, Subject: c.Subject.CommonName, Issuer: c.Issuer.CommonName}
}
