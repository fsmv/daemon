package embedportal

import (
	"context"
	"crypto"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"

	"ask.systems/daemon/tools"
	"golang.org/x/crypto/acme"
)

type acmeChallenges struct {
	sync.Map
}

func (c *acmeChallenges) Write(path string, value string) {
	c.Map.Store(path, value)
}

func (c *acmeChallenges) Read(path string) (string, bool) {
	value, ok := c.Map.Load(path)
	if !ok {
		return "", false
	}
	return value.(string), ok
}

func (c *acmeChallenges) Delete(path string) {
	c.Map.Delete(path)
}

func loadCACert(certFile string) *x509.CertPool {
	certBytes, err := os.ReadFile(certFile)
	if err != nil {
		log.Print("Error reading ACME_SERVER_CERT file: ", err)
		return nil
	}
	block, _ := pem.Decode(certBytes)
	if block == nil {
		log.Print("Error decoding ACME_SERVER_CERT file.")
		return nil
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		log.Print("Error parsing ACME_SERVER_CERT file: ", err)
		return nil
	}
	pool, err := x509.SystemCertPool()
	if err != nil {
		log.Print("Error reading system CA roots: ", err)
		return nil
	}
	pool.AddCert(cert)
	return pool
}

func acmeClient(accountKey crypto.Signer) *acme.Client {
	client := http.DefaultClient
	if serverCertFile := os.Getenv("ACME_SERVER_CERT"); serverCertFile != "" {
		log.Print("Loading test CA root file for ACME server testing: ", serverCertFile)
		roots := loadCACert(serverCertFile)
		if roots != nil {
			transport := http.DefaultTransport.(*http.Transport).Clone()
			transport.TLSClientConfig = &tls.Config{
				RootCAs: roots,
			}
			client = &http.Client{
				Transport: transport,
			}
		}
	}
	return &acme.Client{
		Key:          accountKey,
		DirectoryURL: kACMEAddress,
		HTTPClient:   client,
		UserAgent:    "daemon portal",
	}
}

// Make an account
// TODO: should I bother adding the email address here?
func fetchACMEAccount(client *acme.Client) (*acme.Account, error) {
	ctx := context.Background()
	account, err := client.GetReg(ctx, "")
	if err != nil {
		if errors.Is(err, acme.ErrNoAccount) {
			account, err = client.Register(ctx, &acme.Account{}, acme.AcceptTOS)
			if err != nil {
				return nil, fmt.Errorf("acme.Register error: %w", err)
			}
		} else {
			return nil, fmt.Errorf("acme.GetReg error: %w", err)
		}
	}
	return account, nil
}

// Calls client.Discover and returns the results
// Also logs the CAA record information and TOS link
func logCAARecord(domain string, client *acme.Client, account *acme.Account) (d acme.Directory, err error) {
	ctx := context.Background()
	d, err = client.Discover(ctx)
	if err != nil {
		return
	}
	log.Print("By using automatic certs you agree to the CA's TOS: ", d.Terms)
	if len(d.CAA) > 0 {
		log.Printf("For additional security create a DNS CAA record for %v containing: %q",
			domain, fmt.Sprintf("%v;accounturi=%v", d.CAA[0], account.URI))
	}
	return
}

func obtainACMECert(domain string, client *acme.Client, account *acme.Account, challenges *acmeChallenges) (*tls.Certificate, error) {
	ctx := context.Background()
	directory, err := logCAARecord(domain, client, account)
	if err != nil {
		return nil, fmt.Errorf("acme.Discover error: %w", err)
	}
	log.Printf("Registering a certificate for %v with: %v", domain, directory.Website)
	// Start the challenge process
	order, err := client.AuthorizeOrder(ctx, acme.DomainIDs(domain))
	if err != nil {
		return nil, fmt.Errorf("acme.AuthorizeOrder error: %w", err)
	}
	if order.Status != acme.StatusPending {
		return nil, fmt.Errorf("invalid new order status %q", order.Status)
	}
	// Complete all pending authorizations (contains challenges)
	for _, authz := range order.AuthzURLs {
		z, err := client.GetAuthorization(ctx, authz)
		if err != nil {
			return nil, fmt.Errorf("acme.GetAuthorization error: %w", err)
		}
		if z.Status != acme.StatusPending {
			continue
		}
		// Select the http one, we only have to do one
		// TODO: could someday use tls-alpn-01
		// The benefit would be that we can make port 80 completely optional and
		// not even start an HTTP server but still allow cert challenges. You would
		// still be required to use port 443 for the challenge though.
		//
		// The protocol is designed so that it doesn't matter that HTTP isn't
		// encrypted for the challenge.
		var httpChallenge *acme.Challenge
		for _, c := range z.Challenges {
			if c.Type == "http-01" {
				httpChallenge = c
				break
			}
		}
		if httpChallenge == nil {
			return nil, errors.New("no http challenge found")
		}
		// Get the challenge parameters
		resp, err := client.HTTP01ChallengeResponse(httpChallenge.Token)
		if err != nil {
			return nil, fmt.Errorf("acme.HTTP01ChallengeResponse error: %w", err)
		}
		path := client.HTTP01ChallengePath(httpChallenge.Token)
		// Serve the parameters live on port 80
		challenges.Write(path, resp)
		defer challenges.Delete(path)
		// Tell the authority we're ready
		if _, err := client.Accept(ctx, httpChallenge); err != nil {
			return nil, fmt.Errorf("acme.Accept error: %w", err)
		}
		if _, err := client.WaitAuthorization(ctx, z.URI); err != nil {
			return nil, fmt.Errorf("acme.WaitAuthorization error: %w", err)
		}
	}
	order, err = client.WaitOrder(ctx, order.URI)
	if err != nil {
		return nil, fmt.Errorf("acme.WaitOrder error: %w", err)
	}

	// Now create the cert
	csr, certKey, err := tools.GenerateCertificateRequest(domain)
	if err != nil {
		return nil, fmt.Errorf("failed to generate certificate request: %w", err)
	}
	der, _, err := client.CreateOrderCert(ctx, order.FinalizeURL, csr, true)
	if err != nil {
		return nil, fmt.Errorf("acme.CreateOrderCert error: %w", err)
	}
	return tools.TLSCertificateFromBytes(der, certKey)
}
