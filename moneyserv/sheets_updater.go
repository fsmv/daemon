package main

import (
    "os"
    "log"
    "fmt"
    "sync"
    "encoding/gob"
    "net/http"
)

const (
    baseURL = "https://serv.sapium.net"
    callbackPath = "/money/auth/callback"
    loginPath = "/money/auth"
    tokenFile = "oauth_tokens"
)

type SheetsUpdater struct {
    client    OAuthClient
    tokenMut  *sync.RWMutex
    token     OAuthToken
    hasToken  bool
}

func InitSheetsUpdater(oauthClientId, oauthClientSecret string) *SheetsUpdater {
    s := &SheetsUpdater{
        client: OAuthClient{
            ID:          oauthClientId,
            Secret:      oauthClientSecret,
            RedirectURI: baseURL + callbackPath,
        },
        tokenMut: &sync.RWMutex{},
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
    s.tokenMut.Lock()
    err = d.Decode(&s.token)
    s.tokenMut.Unlock()
    if err != nil {
        log.Printf("Failed to decode token file: %v", err)
        return
    }
    log.Printf("Loaded auth token from file")
    s.hasToken = true
}

func (s *SheetsUpdater) saveToken(token OAuthToken) {
    // Store in memory
    s.tokenMut.Lock()
    s.token = token
    s.tokenMut.Unlock()
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

func (s *SheetsUpdater) genXSRFToken() string {
    // TODO
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
        redirectURL := s.client.GetRedirectURL(
            "https://accounts.google.com/o/oauth2/v2/",
            "https://www.googleapis.com/auth/spreadsheets",
            s.genXSRFToken())
        http.Redirect(w, r, redirectURL, http.StatusSeeOther)
    })
    callbackHandler := s.client.NewCallbackHandler(
        "https://www.googleapis.com/oauth2/v4/token",
        // TODO: interface for these functions
        func (token string) bool {
            return s.checkXSRFToken(token)
        },
        func (token OAuthToken) {
            s.saveToken(token)
        })
    http.HandleFunc(callbackPath, func(w http.ResponseWriter, r *http.Request) {
        if s.hasToken {
            http.NotFound(w, r)
        }
        callbackHandler.ServeHTTP(w, r)
        // Success!
        if w /* TODO: check closed */ {
            return
        }
        fmt.Fprint(w, "<html><body><h2>Login sucessful!</h2></body></html>")
    })
}
