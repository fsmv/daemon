package main

import (
    "flag"
    "log"
    "sync"
    "os"
    "os/signal"
    "context"

    "ask.systems/daemon/feproxy"
)

var (
    feproxyAddr = flag.String("feproxy_addr", "127.0.0.1:2048",
        "Address and port for the feproxy server")
    registrations feproxy.RegisterRequests
)

func init() {
    flag.Var(&registrations, "+",
        "A JSON flag for mapping third party web servers to a url path.\n." +
        "Uses the feproxy/service.proto RegisterRequests json format.\n" +
        "For example:" + `'{"requests": [{"fixed_port": 1337, "pattern" : "/your-server", "strip_pattern": true}, {...}, ...]}'`)
    flag.Parse()
}

func main() {
    var wg sync.WaitGroup
    quit := make(chan struct{})
    fe, err := feproxy.Connect(*feproxyAddr)
    if err != nil {
      log.Fatal(err)
    }
    errCount := 0
    for i, registration := range registrations.Requests {
        lease, err := fe.RPC.Register(context.Background(), registration)
        if err != nil {
          log.Printf("Failed to register #%v %+v: %v", i, registration, err)
          errCount++
          continue
        }
        log.Printf("Obtained lease for %#v, port: %v, timeout: %v",
            lease.Pattern, lease.Port, lease.Timeout.AsTime())
        wg.Add(1)
        go func () {
            fe.KeepLeaseRenewed(quit, lease)
            wg.Done()
        }()
    }
    if errCount == len(registrations.Requests) {
      close(quit)
      wg.Wait()
      log.Fatal("None of the registrations were successful.")
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
