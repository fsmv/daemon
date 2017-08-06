package main

import (
    "os"
    "os/signal"
    "log"

    "feproxy/proxyserv"
    "feproxy/rpcserv"
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
        quit <- struct{}{}
    }()

    proxySrv := proxyserv.StartNew(tlsCert, tlsKey,
        portRangeStart, portRangeEnd, leaseTTL, quit)
    log.Print("Started frontend proxy server")

    rpcserv.StartNew(proxySrv, rpcPort, quit)
    log.Print("Started rpc server on port ", rpcPort)

    <-quit // Wait for quit
}
