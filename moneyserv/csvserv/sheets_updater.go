package csvserv

import (
    "os"
    "io/ioutil"
    "log"
    "fmt"
    "time"
    "sync"
    "encoding/json"
    "encoding/gob"
    "net/http"
    "net/url"
)

const (
    baseURL = "https://serv.sapium.net"
    callbackPath = "/money/auth/callback"
    loginPath = "/money/auth"
    tokenFile = "oauth_tokens"
)

type oauthClient struct {
    ID           string
    Secret       string
    RedirectURI  string
}

type oauthToken struct {
    Access  string
    Refresh string
    Timeout time.Time
}

type SheetsUpdater struct {
    client    oauthClient
    tokensMut *sync.RWMutex
    token     oauthToken
    hasToken  bool
}

func New(oauthClientId, oauthClientSecret string) *SheetsUpdater{
    s := &SheetsUpdater{
        client: oauthClient{
            ID: oauthClientId,
            Secret: oauthClientSecret,
            RedirectURI: baseURL + callbackPath,
        },
        tokensMut: &sync.RWMutex{},
    }
    s.maybeLoadToken()
    s.registerHandlers()
    return s
}

func (s *SheetsUpdater) maybeLoadToken() {
    f, err := os.Open(tokenFile)
    defer f.Close()
    if err != nil {
        return
    }
    d := gob.NewDecoder(f)
    s.tokensMut.Lock()
    err = d.Decode(&s.token)
    s.tokensMut.Unlock()
    if err != nil {
        log.Printf("Failed to decode token file: %v", err)
        return
    }
    log.Printf("Loaded auth token from file")
    s.hasToken = true
}

func (s *SheetsUpdater) saveToken(token oauthToken) {
    // Store in memory
    s.tokensMut.Lock()
    s.token = token
    s.tokensMut.Unlock()
    // Write to file
    f, err := os.Create(tokenFile)
    defer f.Close()
    if err != nil {
        log.Printf("Failed to create token file: %v", err)
        return
    }
    e := gob.NewEncoder(f)
    err = e.Encode(token)
    if err != nil {
        log.Printf("Failed to encode token struct: %v", err)
        return
    }
}

func (s *SheetsUpdater) registerHandlers() {
    if s.hasToken {
        return
    }
    http.HandleFunc(loginPath, func(w http.ResponseWriter, r *http.Request) {
        if s.hasToken {
            fmt.Fprint(w, "<html><body><h2>Already authed!</h2></body></html>")
            return
        }
        scope := url.QueryEscape("https://www.googleapis.com/auth/spreadsheets")
        url := fmt.Sprintf("https://accounts.google.com/o/oauth2/v2/auth?" +
            "access_type=offline&response_type=code&client_id=%v&" +
            "redirect_uri=%v&scope=%v",
            s.client.ID, url.QueryEscape(s.client.RedirectURI), scope)
        http.Redirect(w, r, url, http.StatusSeeOther)
    })
    http.HandleFunc(callbackPath, func(w http.ResponseWriter, r *http.Request) {
        if s.hasToken {
            http.NotFound(w, r)
        }
        // OAuth Auth Callback
        resp := r.URL.Query()
        if errStr, ok := resp["error"]; ok { // Got error
            fmt.Fprintf(w, errStr[0])
            return
        }
        if code, ok := resp["code"]; ok { // Got authorization code
            // Ask OAuth server for refresh and access tokens
            v := url.Values{}
            v.Add("code", code[0])
            v.Add("client_id", s.client.ID)
            v.Add("client_secret", s.client.Secret)
            v.Add("redirect_uri", s.client.RedirectURI)
            v.Add("grant_type", "authorization_code")
            resp, err := http.PostForm("https://www.googleapis.com/oauth2/v4/token", v)
            defer resp.Body.Close()
            if err != nil || resp.StatusCode != http.StatusOK {
                if err != nil {
                    log.Printf("Failed to request OAuth token: %v", err)
                } else {
                    var errorMsgBytes []byte
                    errorMsgBytes, _ = ioutil.ReadAll(resp.Body)
                    log.Printf("Could not get OAuth token.\nRequest: %v\n" +
                        "HTTP error: %v\n%v", v.Encode(),
                        resp.Status, string(errorMsgBytes))
                }
                http.Error(w, "Failed to contact oauth server",
                           http.StatusInternalServerError)
                return
            }
            // Got refresh and access token response, parse it
            var oauthResp struct {
                access_token  string
                expires_in    int
                token_type    string
                refresh_token string
            }
            err = json.NewDecoder(resp.Body).Decode(&oauthResp)
            if err != nil {
                log.Printf("Response from auth server invalid. err: %v", err)
                http.Error(w, "Invalid response from oauth server",
                           http.StatusInternalServerError)
                return
            }
            // Store the access and refresh token in our struct
            s.saveToken(oauthToken{
                Access:  oauthResp.access_token,
                Refresh: oauthResp.refresh_token,
                Timeout: time.Now().Add(time.Second * time.Duration(oauthResp.expires_in)),
            })
            // Success!
            fmt.Fprint(w, "<html><body><h2>Login sucessful!</h2></body></html>")
            return
        }
        http.NotFound(w, r)
    })
}
