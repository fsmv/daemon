package embedportal

import (
	"context"
	"crypto"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
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

func acmeClient(accountKey crypto.Signer) *acme.Client {
	return &acme.Client{
		Key:          accountKey,
		DirectoryURL: acme.LetsEncryptURL,
		UserAgent:    "daemon portal",
	}
}

func fetchACMEAccount(client *acme.Client) (*acme.Account, error) {
	ctx := context.Background()
	account, err := client.GetReg(ctx, "")
	if errors.Is(err, acme.ErrNoAccount) {
		account, err = client.Register(ctx, &acme.Account{}, acme.AcceptTOS)
		if err != nil {
			err = fmt.Errorf("acme.Register error: %w", err)
		}
	} else {
		err = fmt.Errorf("acme.GetReg error: %w", err)
	}
	if err != nil {
		return nil, err
	}
	return account, nil
}

func obtainACMECert(domain string, client *acme.Client, account *acme.Account, challenges *acmeChallenges) (*tls.Certificate, error) {
	ctx := context.Background()
	directory, err := client.Discover(ctx)
	if err != nil {
		return nil, fmt.Errorf("acme.Discover error: %w", err)
	}
	// Make an account (can add email here)
	log.Printf("Registering a certificate for %v with: %v", domain, directory.Website)
	log.Print("By using automatic certs you agree to the CA's TOS: ", directory.Terms)
	if len(directory.CAA) > 0 {
		log.Printf("For additional security create a DNS CAA record for %v containing: %q",
			domain, fmt.Sprintf("%v;accounturi=%v", directory.CAA[0], account.URI))
	}
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
