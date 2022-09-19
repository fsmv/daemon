package tools

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log"
	"math/big"
	"net"
	"sync/atomic"
	"time"
)

// Read only thread safe store for the latest certificate that will be
// automatically updated when renewed.
type AutorenewCertificate struct {
	cache *atomic.Value
}

func (c *AutorenewCertificate) Certificate() *tls.Certificate {
	return c.cache.Load().(*tls.Certificate)
}

func AutorenewSelfSignedCertificate(hostname string, TTL time.Duration, onRenew func(*tls.Certificate), quit chan struct{}) (*AutorenewCertificate, error) {
	cache := &atomic.Value{}
	newCert, err := GenerateSelfSignedCertificate(hostname, time.Now().Add(TTL))
	if err != nil {
		return nil, err
	}
	if onRenew != nil {
		onRenew(newCert)
	}
	cache.Store(newCert)

	go func() {
		timer := time.NewTimer(TTL / 2)
		for {
			select {
			case <-quit:
				timer.Stop()
				return
			case <-timer.C:
			}
			newCert, err := GenerateSelfSignedCertificate(hostname, time.Now().Add(TTL))
			if err != nil {
				log.Print("Failed to renew self signed certificate: ", err)
				continue
			}
			if onRenew != nil {
				onRenew(newCert)
			}
			cache.Store(newCert)
			timer.Reset(TTL / 2)
		}
	}()

	return &AutorenewCertificate{cache}, nil
}

func GenerateSelfSignedCertificate(hostname string, expiration time.Time) (*tls.Certificate, error) {
	csr, private, err := GenerateCertificateRequest(hostname)
	if err != nil {
		return nil, err
	}
	signedCert, err := SignCertificate(&tls.Certificate{PrivateKey: private}, csr, expiration, true)
	if err != nil {
		return nil, err
	}
	return CertificateFromSignedCert(signedCert, private), nil
}

func GenerateCertificateRequest(hostname string) ([]byte, *ecdsa.PrivateKey, error) {
	template := &x509.CertificateRequest{
		SignatureAlgorithm: x509.ECDSAWithSHA256,
	}
	if ip := net.ParseIP(hostname); ip != nil {
		template.IPAddresses = []net.IP{ip}
	} else {
		template.DNSNames = []string{hostname}
	}
	private, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, private, err
	}
	csr, err := x509.CreateCertificateRequest(rand.Reader, template, private)
	return csr, private, err
}

func SignCertificate(root *tls.Certificate, rawCertRequest []byte, expiration time.Time, isCA bool) ([]byte, error) {
	csr, err := x509.ParseCertificateRequest(rawCertRequest)
	if err != nil {
		return nil, fmt.Errorf("Error parsing CSR: %w", err)
	}
	if err := csr.CheckSignature(); err != nil {
		return nil, fmt.Errorf("Error validating CSR signature: %w", err)
	}
	serial, err := rand.Int(rand.Reader, big.NewInt(0).SetBit(big.NewInt(0), 128, 1))
	if err != nil {
		return nil, fmt.Errorf("Error generating serial number: %w", err)
	}
	template := &x509.Certificate{
		SerialNumber: serial,
		NotBefore:    time.Now(),
		NotAfter:     expiration,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},

		SignatureAlgorithm: csr.SignatureAlgorithm,
		Subject:            csr.Subject,
		DNSNames:           csr.DNSNames,
		EmailAddresses:     csr.EmailAddresses,
		IPAddresses:        csr.IPAddresses,
		URIs:               csr.URIs,
		ExtraExtensions:    csr.ExtraExtensions,
	}
	if isCA {
		template.IsCA = true
		template.KeyUsage = x509.KeyUsageCertSign | x509.KeyUsageCRLSign
		template.MaxPathLen = 2
		template.BasicConstraintsValid = true
	}
	var parent *x509.Certificate
	if root.Certificate == nil {
		// Do a self signed cert. Note: root.Private is still needed
		parent = template
	} else {
		parent, err = x509.ParseCertificate(root.Certificate[0])
		if err != nil {
			return nil, fmt.Errorf("Error parsing parent cert: %w", err)
		}
		template.SignatureAlgorithm = parent.SignatureAlgorithm
	}
	cert, err := x509.CreateCertificate(rand.Reader,
		template, parent, csr.PublicKey, root.PrivateKey)
	if err != nil {
		return cert, fmt.Errorf("Error creating certificate: %w", err)
	}
	return cert, nil
}

func CertificateFromSignedCert(rawCert []byte, privateKey *ecdsa.PrivateKey) *tls.Certificate {
	return &tls.Certificate{
		Certificate:                  [][]byte{rawCert},
		PrivateKey:                   privateKey,
		SupportedSignatureAlgorithms: []tls.SignatureScheme{tls.ECDSAWithP256AndSHA256},
	}
}
