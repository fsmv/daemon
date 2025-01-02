package tools

import (
	"crypto/sha256"
	"encoding/base64"
	"testing"
)

const TestPassword = "hunter2"

func TestCheckPassword(t *testing.T) {
	if !CheckPassword(HashPassword(TestPassword), TestPassword) {
		t.Errorf("Password didn't match")
	}
	if CheckPassword(HashPassword(TestPassword), TestPassword+"!") {
		t.Errorf("Password shouldn't have matched!")
	}
}

// The original standard library only hash function I used for basic auth,
// before I bit the bullet and added the golang.org/x/crypto dependency.
//
// Copied from commit 54703cb6ed798d064e989b26a7586fbe83e3c05b
func oldBasicAuthHash(password string) string {
	hash := sha256.Sum256([]byte(password))
	return base64.URLEncoding.EncodeToString(hash[:])
}

func TestOldBasicAuthPassword(t *testing.T) {
	if !CheckPassword(oldBasicAuthHash(TestPassword), TestPassword) {
		t.Errorf("Password didn't match")
	}
	if CheckPassword(oldBasicAuthHash(TestPassword), TestPassword+"!") {
		t.Errorf("Password shouldn't have matched!")
	}
}
