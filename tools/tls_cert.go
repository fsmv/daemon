package tools

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log"
	"math/big"
	"net"
	"sync/atomic"
	"time"
)

// Generate a new self signed certificate for the given hostname with the given
// TTL expiration time, and keep it renewed in the background until the quit
// channel is closed.
//
// If isCA is true, set the capability bits to be a root Certificate Authority.
// So you can use the cert with [SignCertificate]. Certificate Authority certs
// cannot be used to serve webpages.
//
// If the onRenew function is not nil, it is called every time the certificate
// is renewed, including the first time it is generated.
//
// The returned config only has [tls.Config.GetCertificate] set, and it will
// return the latest certificate for any arguments (including nil).
func AutorenewSelfSignedCertificate(hostname string, TTL time.Duration, isCA bool, onRenew func(*tls.Certificate), quit chan struct{}) (*tls.Config, error) {
	cache := &atomic.Value{}
	newCert, err := GenerateSelfSignedCertificate(hostname, time.Now().Add(TTL), isCA)
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
			newCert, err := GenerateSelfSignedCertificate(hostname, time.Now().Add(TTL), isCA)
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

	return &tls.Config{
		GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
			cert, ok := cache.Load().(*tls.Certificate)
			if !ok || cert == nil {
				return nil, errors.New("Failed to load self-signed certificate.")
			}
			return cert, nil
		},
	}, nil
}

// Generate a self signed TLS certificate for the given hostname and expiration
// date.
//
// If isCA is true, set the capability bits to be a root Certificate Authority.
// So you can use the cert with [SignCertificate]. Certificate Authority certs
// cannot be used to serve webpages.
func GenerateSelfSignedCertificate(hostname string, expiration time.Time, isCA bool) (*tls.Certificate, error) {
	csr, private, err := GenerateCertificateRequest(hostname)
	if err != nil {
		return nil, err
	}
	signedCert, err := SignCertificate(&tls.Certificate{PrivateKey: private}, csr, expiration, isCA)
	if err != nil {
		return nil, err
	}
	return TLSCertificateFromBytes([][]byte{signedCert}, private)
}

// Generate a random certificate key and a request to send to a Certificate
// Authority to get your new certificate signed.
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

// Use a root Certificate Authority certificate to sign a given certificate
// request and give the new certificate the specified expiration date.
//
// Returns the raw certificate data from [crypto/x509.CreateCertificate].
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

// Convert raw certificate bytes and a private key into the [tls.Certificate]
// structure, so it can be used for go connections.
//
// Deprecated: It's better to use [TLSCertificateFromBytes] because that
// function returns the validation errors while this one ignores them.
func CertificateFromSignedCert(rawCert []byte, privateKey crypto.Signer) *tls.Certificate {
	cert, _ := TLSCertificateFromBytes([][]byte{rawCert}, privateKey)
	return cert
}

// Convert raw certificate bytes and a private key into the [tls.Certificate]
// structure, so it can be used for go connections.
//
// der is x509 certificate pem blocks. It is a 2-level array for certificate
// chains. Self signed certs and certs with one CA only need the one public key
// in the chain.
//
// This is similar to [tls.X509KeyPair] but the pem blocks and privateKey are
// pre-parsed.
//
// Returns an error if the certificate data is invalid or doesn't match the
// privateKey
func TLSCertificateFromBytes(der [][]byte, privateKey crypto.Signer) (*tls.Certificate, error) {
	leaf, err := parseAndValidateCert(der, privateKey)
	if err != nil {
		// Return this for compatibility with CertificateFromSignedCert's old
		// behavior of not checking validation
		return &tls.Certificate{
			Certificate: der,
			PrivateKey:  privateKey,
		}, err
	}
	return &tls.Certificate{
		Certificate: der,
		PrivateKey:  privateKey,
		Leaf:        leaf,
	}, nil
}

// All standard library crypto.PublicKey types support this interface.
// See: https://pkg.go.dev/crypto#PublicKey
type stdPublicKey interface {
	Equal(x crypto.PublicKey) bool
}

func parseAndValidateCert(der [][]byte, privateKey crypto.Signer) (leaf *x509.Certificate, err error) {
	for i, block := range der {
		cert, err := x509.ParseCertificate(block)
		if err != nil {
			return nil, err
		}
		if i == 0 {
			leaf = cert
		}
	}
	if leaf == nil {
		return nil, errors.New("no certificates found")
	}
	// Check the expiration date
	if now := time.Now(); now.Before(leaf.NotBefore) || now.After(leaf.NotAfter) {
		return nil, errors.New("certificate time range is not valid")
	}
	// Check that the keys match
	pub, ok := leaf.PublicKey.(stdPublicKey)
	if !ok {
		return nil, errors.New("crypto.PublicKey has no Equal method")
	}
	if !pub.Equal(privateKey.Public()) {
		return nil, errors.New("private key does not match public key")
	}
	return leaf, nil
}
