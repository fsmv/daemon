package main

import (
    "encoding/json"
    "flag"
    "log"
    "sync"
    "os"
    "os/signal"
    "fmt"

    "daemon/feproxy/proxyserv"
)

var (
    feproxyAddr = flag.String("feproxy_addr", "127.0.0.1:2048",
        "Address and port for the feproxy server")
    mappings Mappings
)

func init() {
    flag.Var(&mappings, "mappings",
        "A JSON flag for mapping third party web servers to a url path.\n\t" +
        "For example:" + `'[{"Port": 1337, "UrlPattern" : "/your-server"}, {...}, ...]'`)
    flag.Parse()
}

type Mappings []Mapping
type Mapping struct {
    Port uint16 // The port the server is running on. Must not be in the our portRangeStart to portRangeEnd
    UrlPattern string // The URL on the domain to forward to the above host
}

func (f *Mappings) Set(input string) error {
    err := json.Unmarshal([]byte(input), f)
    if err != nil {
        return err
    }
    for i, mapping := range *f {
        if mapping.Port == 0 {
            return fmt.Errorf("Missing Port in index %v", i)
        }
        if mapping.UrlPattern == "" {
            return fmt.Errorf("Missing UrlPattern in index %v", i)
        }
    }
    return nil
}

func (f *Mappings) String() string {
    if f == nil {
        return "nil"
    }
    return fmt.Sprintf("%+v", []Mapping(*f))
}

func main() {
    var wg sync.WaitGroup
    quit := make(chan struct{})
    // Register the mappings with feproxy and start the threads to renew the leases
    for _, mapping := range mappings {
        fe, lease := proxyserv.MustConnectAndRegisterThirdParty(*feproxyAddr,
            mapping.Port, mapping.UrlPattern)
        wg.Add(1)
        go func () {
            fe.KeepLeaseRenewed(quit, lease)
            wg.Done()
        }()
    }

    // Just wait util we receive a signal to shut down
    sigs := make(chan os.Signal, 2)
    signal.Notify(sigs, os.Interrupt, os.Kill)
    switch <-sigs {
    case os.Interrupt:
        log.Print("Recieved keyboard interrupt")
    case os.Kill:
        log.Print("Recieved kill signal")
    }

    log.Print("Shutting down...")
    close(quit)
    wg.Wait()
    log.Print("Goodbye.")
}
