package tools

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

// Returns a random base64 string of the specified number of bytes.
// If there's an error calling [crypto/rand.Read], it returns "".
//
// Uses [base64.URLEncoding] for URL safe strings.
func RandomString(bytes int) string {
	b := make([]byte, bytes)
	_, err := rand.Read(b)
	if err != nil {
		return ""
	}
	return base64.URLEncoding.EncodeToString(b)
}

// The error type returned by HashPassword when the password is too long for the
// hash function. This will be updated if the password hash function changes.
var ErrPasswordTooLong = bcrypt.ErrPasswordTooLong

// Returns a password hash compatible with the default [BasicAuthHandler] hash.
// May change algorithms over time as hash recommendations change.
//
// Returns an empty string if there's any error: the password is too long for
// the current hash function, failed reading random devices, etc.
//
// Check for [ErrPasswordTooLong] with [errors.Is] to display to the user.
func HashPassword(password string) (string, error) {
	// This generates a salt internally and produces the standard encoded string
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

// Checks passwords for [BasicAuthHandler] (or other uses if you want). Accepts
// hashes from [HashPassword] and will continue to accept hashes from old
// versions for compatibility. Empty authHash always returns false.
func CheckPassword(authHash, userPassword string) bool {
	if authHash == "" {
		return false
	}
	// The first good version, bcrypt
	if strings.HasPrefix(authHash, "$2") {
		return (bcrypt.CompareHashAndPassword([]byte(authHash), []byte(userPassword)) == nil)
	}
	// Check the format from the first version of HashPassword which used only
	// the standard library. I didn't think much before I picked sha256 but it's
	// not a good idea given all the bitcoin mining rigs out there.
	//
	// We can detect the format I used because most others use based64.StdEncoding
	if wantHash, err := base64.URLEncoding.DecodeString(authHash); err == nil {
		hash := sha256.Sum256([]byte(userPassword))
		return (1 == subtle.ConstantTimeCompare(hash[:], wantHash))
	}
	return false
}
