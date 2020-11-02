package main

import (
    "flag"
    "fmt"
    "encoding/json"
    "log"
    "net"
    "os"
    "os/signal"
    "strconv"

    "daemon/feproxy/proxyserv"
    "daemon/feproxy/rpcserv"
)

const (
    rpcPort = 2048
    portRangeStart = 2049
    portRangeEnd = 4096
    leaseTTL = "24h"
)

var (
    tlsCertPath = flag.String("tls_cert", "",
        "Either the filepath to the tls cert file (fullchain.pem) or\n\t" +
        "the file descriptor id number shared by the parent process")
    tlsKeyPath = flag.String("tls_key", "",
        "Either the filepath to the tls key file (privkey.pem) or\n\t" +
        "the file descriptor id number shared by the parent process")
    httpPortSpec = flag.Int("http_port", 80,
        "If positive, the port to bind to for http traffic or\n\t" +
        "if negative, the file descriptor id for a socket to listen on\n\t" +
        "shared by the parent process.")
    httpsPortSpec = flag.Int("https_port", 443,
        "If positive, the port to bind to for https traffic or\n\t" +
        "if negative, the file descriptor id for a socket to listen on\n\t" +
        "shared by the parent process.")
    staticMappings StaticMappings
)

func init() {
    flag.Var(&staticMappings, "static_mappings",
        "A JSON flag for any extra ports that need to be mapped to a URL.\n\t" +
        "For example if there's a third-party server with a web interface\n\t" +
        "you want behind the SSL cert.\n\t" +
        `Ex: '[{"Host": "string", "Port": 1337, "UrlPattern" : "/your-server"}, {...}, ...]'`)
    flag.Parse()
}

type StaticMappings []StaticMapping
type StaticMapping struct {
    Host string // The address of the server behind the reverse proxy e.g. "localhost"
    Port uint16 // The port the server is running on. Must not be in the our portRangeStart to portRangeEnd
    UrlPattern string // The URL on the domain to forward to the above host
}

func (f *StaticMappings) Set(input string) error {
    err := json.Unmarshal([]byte(input), f)
    if err != nil {
        return err
    }
    for i, mapping := range *f {
        if mapping.Host == "" {
            return fmt.Errorf("Missing Host in index %v", i)
        }
        if mapping.Port == 0 {
            return fmt.Errorf("Missing Port in index %v", i)
        }
        if mapping.UrlPattern == "" {
            return fmt.Errorf("Missing UrlPattern in index %v", i)
        }
        if mapping.Port >= portRangeStart && mapping.Port <= portRangeEnd {
            return fmt.Errorf("Port (%v, in index: %v) cannot be in the reserved range [%v, %v]",
                mapping.Port, i, portRangeStart, portRangeEnd)
        }
    }
    return nil
}

func (f *StaticMappings) String() string {
    if f == nil {
        return "nil"
    }
    return fmt.Sprintf("%+v", []StaticMapping(*f))
}

func openFilePathOrFD(pathOrFD string) (*os.File, error) {
    if fd, err := strconv.Atoi(pathOrFD); err == nil {
        return os.NewFile(uintptr(fd), ""), nil
    }
    return os.Open(pathOrFD)
}

func listenerFromPortOrFD(portOrFD int) (net.Listener, error) {
    if portOrFD < 0 {
        return net.FileListener(os.NewFile(uintptr(-portOrFD), ""))
    }
    return net.Listen("tcp", fmt.Sprintf(":%v", portOrFD))
}

func main() {
    quit := make(chan struct{})
    sigs := make(chan os.Signal, 2)
    signal.Notify(sigs, os.Interrupt, os.Kill)
    go func() {
        <-sigs
        close(quit)
    }()

    tlsCert, err := openFilePathOrFD(*tlsCertPath)
    if err != nil {
        log.Fatalf("Failed to load tls cert file (%v): %v",
            *tlsCertPath, err)
    }
    tlsKey, err := openFilePathOrFD(*tlsKeyPath)
    if err != nil {
        log.Fatalf("Failed to load tls key file (%v): %v",
            *tlsKeyPath, err)
    }
    httpListener, err := listenerFromPortOrFD(*httpPortSpec)
    if err != nil {
        log.Fatalf("Failed to listen on http port (%v): %v",
            *httpPortSpec, err)
    }
    httpsListener, err := listenerFromPortOrFD(*httpsPortSpec)
    if err != nil {
        log.Fatalf("Failed to listen on https port (%v): %v",
            *httpsPortSpec, err)
    }

    proxySrv, err := proxyserv.StartNew(
        tlsCert, tlsKey, httpListener, httpsListener,
        portRangeStart, portRangeEnd, leaseTTL, quit)
    log.Print("Started frontend proxy server")
    if err != nil {
        log.Fatalf("Failed to start proxyserv: %v", err)
    }

    for i, mapping := range staticMappings {
        err := proxySrv.RegisterStatic(mapping.Host, mapping.Port, mapping.UrlPattern)
        if err != nil {
            log.Fatal("Failed to register static mapping (index: %v, %+v): %v", i, mapping, err)
        }
    }

    _, err = rpcserv.StartNew(proxySrv, rpcPort, quit)
    log.Print("Started rpc server on port ", rpcPort)
    if err != nil {
        log.Fatal(err)
    }

    <-quit // Wait for quit
}
