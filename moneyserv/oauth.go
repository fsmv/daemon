// TODO: comments
package main

import (
    "bytes"
    "encoding/json"
    "fmt"
    "io"
    "io/ioutil"
    "log"
    "net/http"
    "net/url"
    "time"
)

type OAuthClient struct {
    ID           string
    Secret       string
    RedirectURI  string
}

type OAuthToken struct {
    Access  string
    Refresh string
    Timeout time.Time
}

func (c *OAuthClient) GetRedirectURL(authURL, scope, xsrfToken string) string {
    v := url.Values{
        "access_type": {"offline"},
        "response_type": {"code"},
        "client_id": {c.ID},
        "redirect_uri": {c.RedirectURI},
        "scope": {scope},
        "state": {xsrfToken},
    }
    return url.PathEscape(authURL) + v.Encode()
}

type OAuthCallbackHandler struct {
    Client         *OAuthClient
    tokenReqURL    string
    checkXSRFToken func(string) bool
    successfulAuth func(OAuthToken)
}

func (c *OAuthClient) NewCallbackHandler(tokenReqURL string,
    checkXSRFToken func(string) bool,
    successfulAuth func(OAuthToken)) *OAuthCallbackHandler {

    return &OAuthCallbackHandler{
        Client:         c,
        tokenReqURL:    tokenReqURL,
        checkXSRFToken: checkXSRFToken,
        successfulAuth: successfulAuth,
    }
}

// Ask OAuth server for refresh and access tokens
func (c *OAuthClient) requestToken(host, authorizationCode string,
    token *OAuthToken) error {

    // Send Request
    v := url.Values{
        "grant_type":    []string{"authorization_code"},
        "client_id":     []string{c.ID},
        "client_secret": []string{c.Secret},
        "redirect_uri":  []string{c.RedirectURI},
        "code":          []string{authorizationCode},
    }
    resp, err := http.PostForm(host, v)
    defer resp.Body.Close()
    if err != nil {
        return err
    }
    if resp.StatusCode != http.StatusOK {
        var errorMsgBytes []byte
        errorMsgBytes, _ = ioutil.ReadAll(resp.Body)
        return fmt.Errorf("Error from OAuth server.\n" +
            "Sent: %v\nStatus: %v\nMessage: %v",
            host + v.Encode(), resp.Status, string(errorMsgBytes))
    }
    // Got refresh and access token response, parse it
    var tokenResp struct {
        access_token  string
        expires_in    int
        token_type    string
        refresh_token string
    }
    var bodyBuff bytes.Buffer
    tee := io.TeeReader(resp.Body, &bodyBuff)
    err = json.NewDecoder(tee).Decode(&tokenResp)
    if err != nil {
        // Finish reading the tee to copy everything to the buffer
        for b, err := make([]byte, 512), error(nil); err == nil; {
            _, err = tee.Read(b)
        }
        return fmt.Errorf("Response from auth server could not be decoded.\n"+
            "Sent: %v\nDecode Error: %v\nResponse:\n%v",
            host + v.Encode(), err, bodyBuff)
    }
    // Return result
    token.Access = tokenResp.access_token
    token.Refresh = tokenResp.refresh_token
    token.Timeout = time.Now().Add(time.Duration(tokenResp.expires_in) * time.Second)
    return nil
}

func (h *OAuthCallbackHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    // TODO: log requests on error
    q := r.URL.Query()
    if errStr := q.Get("error"); errStr != "" {
        http.Error(w, "Recieved error from OAuth server in callback",
                   http.StatusInternalServerError)
        log.Printf("Got error in OAuth callback: %v", errStr)
        return
    }
    if xsrfToken := q.Get("state");
        xsrfToken == "" || !h.checkXSRFToken(xsrfToken) {

        http.Error(w, "Invalid XSRF token", http.StatusForbidden)
        return
    }
    code := q.Get("code")
    if code == "" {
        http.Error(w, "Missing authorization code field", http.StatusBadRequest)
        return
    }
    var token OAuthToken
    err := h.Client.requestToken(h.tokenReqURL, code, &token)
    if err != nil {
        log.Print(err)
        http.Error(w, "Recieved error from OAuth server in token request",
                   http.StatusInternalServerError)
        return
    }
    h.successfulAuth(token)
}
