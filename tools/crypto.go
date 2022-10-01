package tools

import (
	"crypto/rand"
	"encoding/base64"
)

// Returns a random base64 string of the specified number of bytes.
// If there's an error calling [crypto/rand.Read], it returns "".
//
// Uses [encoding/base64.URLEncoding] for URL safe strings.
func RandomString(bytes int) string {
	b := make([]byte, bytes)
	_, err := rand.Read(b)
	if err != nil {
		return ""
	}
	return base64.URLEncoding.EncodeToString(b)
}
