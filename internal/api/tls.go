package api

import (
	"crypto/tls"
	"os"
	"sync"
	"time"
)

// certReloader serves a certificate from PEM files and loads them again
// when they change (e.g. after a Let's Encrypt renewal), checked at most
// once a minute.
type certReloader struct {
	certFile, keyFile string

	mu      sync.Mutex
	cert    *tls.Certificate
	modTime time.Time
	checked time.Time
}

func newCertReloader(certFile, keyFile string) (*certReloader, error) {
	r := &certReloader{certFile: certFile, keyFile: keyFile}
	if err := r.load(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *certReloader) load() error {
	st, err := os.Stat(r.certFile)
	if err != nil {
		return err
	}
	c, err := tls.LoadX509KeyPair(r.certFile, r.keyFile)
	if err != nil {
		return err
	}
	r.cert, r.modTime = &c, st.ModTime()
	return nil
}

func (r *certReloader) get(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if time.Since(r.checked) >= time.Minute {
		r.checked = time.Now()
		if st, err := os.Stat(r.certFile); err == nil && !st.ModTime().Equal(r.modTime) {
			_ = r.load() // keep the old certificate if the new files are incomplete
		}
	}
	return r.cert, nil
}
