package main

import (
    "log"
    "os"
    "os/signal"

    "daemon/feproxy/proxyserv"
    "daemon/feproxy/rpcserv"
)

const (
    rpcPort = 2048
    portRangeStart = 2049
    portRangeEnd = 4096
    tlsCert = "/etc/letsencrypt/live/serv.sapium.net/fullchain.pem"
    tlsKey = "/etc/letsencrypt/live/serv.sapium.net/privkey.pem"
    leaseTTL = "24h"
)

func main() {
    quit := make(chan struct{})
    sigs := make(chan os.Signal, 2)
    signal.Notify(sigs, os.Interrupt, os.Kill)
    go func() {
        <-sigs
        close(quit)
    }()

    proxySrv, err := proxyserv.StartNew(tlsCert, tlsKey,
        portRangeStart, portRangeEnd, leaseTTL, quit)
    log.Print("Started frontend proxy server")
    if err != nil {
        log.Fatal(err)
    }

    _, err = rpcserv.StartNew(proxySrv, rpcPort, quit)
    log.Print("Started rpc server on port ", rpcPort)
    if err != nil {
        log.Fatal(err)
    }

    <-quit // Wait for quit
}
