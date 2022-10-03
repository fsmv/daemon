package tools

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
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

// Returns a [base64.URLEncoding] SHA256 hash of the input password.
// Compatible with [BasicAuthHandler].
func BasicAuthHash(password string) string {
	hash := sha256.Sum256([]byte(password))
	return base64.URLEncoding.EncodeToString(hash[:])
}

// Wraps another [http.Handler] and only calls the wrapped handler if BasicAuth
// passed for one of the registered users. Optionally can call
// [BasicAuthHandler.Check] in as many handlers as you want, and then you don't
// have to use the handler wrapping option.
//
//   - Options must be setup before any requests and then not changed.
//   - Methods may be called at any time, it's thread safe.
type BasicAuthHandler struct {
	// Realm is passed to the browser and the browser will automatically send the
	// same credentials for a realm it has logged into before.
	Realm   string
	Handler http.Handler

	users      sync.Map // map from username string to password hash []byte
	init       sync.Once
	authHeader string
}

// Authorizes the given user to access the pages protected by this handler.
//
// The passwordHash must be a SHA256 [base64.URLEncoding] encoded string. You
// can generate this with [BasicAuthHash].
func (h *BasicAuthHandler) SetUser(username string, passwordHash string) error {
	if username == "" {
		return errors.New("username must not be empty")
	}
	decoded, err := base64.URLEncoding.DecodeString(passwordHash)
	if err != nil {
		return err
	}
	h.users.Store(username, decoded)
	return nil
}

// Authorizes a user with this handler using a "username:password_hash" string
func (h *BasicAuthHandler) SetLogin(login string) error {
	split := strings.Split(login, ":")
	if len(split) != 2 {
		return errors.New("Invalid login string. It should be username:password_hash.")
	}
	return h.SetUser(split[0], split[1])
}

// Unauthorize a given username from pages protected by this handler.
func (h *BasicAuthHandler) RemoveUser(username string) {
	h.users.Delete(username)
}

// Check HTTP basic auth and reply with Unauthorized if authentication failed.
// Returns true if authentication passed and then the users can handle the
// request.
//
// If it returns false auth failed the response has been sent and you can't
// write more.
//
// If you want to log authentication failures, you can use this call instead of
// wrapping your handler.
func (h *BasicAuthHandler) Check(w http.ResponseWriter, r *http.Request) bool {
	// Read the header and if it's not there tell the browser to prompt the user
	username, password, ok := r.BasicAuth()
	if !ok {
		h.init.Do(func() { // Just a cache for the string so we don't malloc every time
			h.authHeader = fmt.Sprintf(`Basic realm="%v", charset="UTF-8"`, h.Realm)
		})
		w.Header().Set("WWW-Authenticate", h.authHeader)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return false
	}
	// Look up the user's password
	wantHashIface, ok := h.users.Load(username)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return false
	}
	wantHash := wantHashIface.([]byte)
	// Hash and check the password
	hash := sha256.Sum256([]byte(password))
	passMatch := (1 == subtle.ConstantTimeCompare(hash[:], wantHash))
	if !passMatch {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return false
	}
	return true // Auth passed
}

// The [http.Handler] interface function. Only calls the wrapped handler if the
// request has passed basic auth.
func (h *BasicAuthHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.Check(w, r) {
		h.Handler.ServeHTTP(w, r) // Auth passed! Call the wrapped handler
	}
}
