package main

import (
    "io"
    "os"
    "os/signal"
    "fmt"
    "flag"
    "log"
    "strings"
    "path/filepath"
    "time"
    "net/http"
    "net/rpc"
    "strconv"

    "daemon/feproxy/proxyserv"
)

var (
    feproxyAddr = flag.String("feproxy_addr", "127.0.0.1:2048",
        "Address and port for the feproxy server")
    dataDir = flag.String("data_dir", "data",
        "Directory to save the daily temperature log files in")
    sensorReadInterval = flag.Duration("sensor_read_interval", 1 * time.Minute,
        "How often to read temperature from the sensor")
)

const (
    register = "/climate"

    w1Dir = "/sys/bus/w1/devices/"
    w1MasterFilename = "w1_bus_master"
    w1SensorFilename = "w1_slave"
    // This is used for 12 bit output mode. I could check the whole message from
    // the sensor to check the mode, but I think the kernel always uses 12 bit.
    w1BitMultiplier = 0.0625
)

func openSensorFile() (*os.File, error) {
    devicesDir, err := os.Open(w1Dir)
    if err != nil {
        return nil, err
    }
    names, err := devicesDir.Readdirnames(-1)
    devicesDir.Close()
    if err != nil {
        return nil, err
    }
    var deviceName string
    for _, name := range names {
        if strings.HasPrefix(name, w1MasterFilename) {
            continue
        }
        deviceName = name
    }
    if deviceName == "" {
        return nil, fmt.Errorf("failed to find a temperature device (looked in %v)",
            w1Dir)
    }
    sensorFilename := filepath.Join(w1Dir, deviceName, w1SensorFilename)
    return os.Open(sensorFilename)
}

// returns the temperature read from the w1 file in Celsius
func readTemperature(sensorFile *os.File) (float32, error) {
    // Read the data
    sensorFile.Sync()
    data := make([]byte, 5)
    _, err := sensorFile.ReadAt(data, 0)
    if err != nil {
        return 0, err
    }
    // Parse the data
    if data[2] != ' ' {
        return 0, fmt.Errorf("Invalid file format: %#v", string(data))
    }
    hexStr := string(append(data[3:5], data[0], data[1]))
    reading, err := strconv.ParseInt(hexStr, 16, 16)
    if err != nil {
        return 0, fmt.Errorf("Invalid file format: %#v", string(data))
    }
    return float32(reading) * w1BitMultiplier, nil;
}

func logTemperature() {
    sensorFile, err := openSensorFile()
    if err != nil {
        log.Printf("Failed to open sensor file")
        return
    }
    if err := os.MkdirAll(*dataDir, os.FileMode(0775)); err != nil {
        log.Printf("Failed to make data directory %#v", *dataDir)
        return
    }
    var once bool
    var currDay int
    var currFile *os.File
    for {
        now := time.Now()
        currTemp, err := readTemperature(sensorFile)
        if err != nil {
            log.Printf("Failed to read temperature")
        }
        if currDay != now.Day() {
            currDay = now.Day()
            currDate := fmt.Sprintf("%04d-%02d-%02d", now.Year(), now.Month(), now.Day())
            currFile.Close()
            currFile, err = os.OpenFile(filepath.Join(*dataDir, currDate),
                os.O_RDWR|os.O_APPEND|os.O_CREATE, os.FileMode(0644))
            if err != nil {
                log.Printf("Failed to open data log file for today")
                return
            }
        }
        currFile.WriteString(fmt.Sprintf("%02d:%02d:%02d, %v\n",
            now.Hour(), now.Minute(), now.Second(), currTemp))
        if !once {
            once = true
            log.Printf("First temperature reading: %v C", currTemp)
        }
        time.Sleep(*sensorReadInterval)
    }
    currFile.Close()
}

type Point struct {
    x, y float32
}
type Polyline []Point

func (l Polyline) Format(f fmt.State, c rune) {
    if len(l) < 2 {
        fmt.Fprint(f, "<!-- error, not enough points -->\n")
        return
    }
    firstPoint := l[0]
    fmt.Fprintf(f, "<polyline fill=\"none\" stroke=\"black\" points=\"%f %f",
        firstPoint.x, firstPoint.y)
    for _, point := range l[1:] {
        fmt.Fprintf(f, ", %f %f", point.x, point.y)
    }
    fmt.Fprint(f, "\"/>\n")
}

func writeSVGGraph(w io.Writer) {
    const indent = "    "
    const line = "<line x1=\"%f\" y1=\"%f\" x2=\"%f\" y2=\"%f\"/>\n"
    Data := []Point{
        {0,0},{100,500},{200,0},{300,500},{400,0},{500,500},
    }
    fmt.Fprintf(w, "<svg width=\"1280\" height=\"720\">\n")
    fmt.Fprintf(w, "%v\n", Polyline(Data))
    fmt.Fprintf(w, "</svg>")
}

func main() {
    flag.Parse()
    sigs := make(chan os.Signal, 2)
    signal.Notify(sigs, os.Interrupt, os.Kill)

    client, err := rpc.Dial("tcp", *feproxyAddr)
    if err != nil {
        log.Fatalf("Failed to connect to frontend proxy RPC server: %v", err)
    }
    defer client.Close()

    var lease proxyserv.Lease
    err = client.Call("feproxy.Register", register, &lease)
    if err != nil {
        log.Fatal("Failed to obtain lease from feproxy:", err)
    }
    log.Printf("Obtained lease, port: %v, ttl: %v", lease.Port, lease.TTL)

    defer client.Call("feproxy.Deregister", register, &lease)
    go func() {
        <-sigs
        //client.Call("feproxy.Deregister", register, &lease)
        log.Print("Shutting down...")
        // TODO(1.8): use http.Server and call close here
        os.Exit(0)
    }()

    go logTemperature()

    http.HandleFunc("/climate", func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Content-Type", "text/html")
        fmt.Fprint(w, "<!doctype html>\n<html><body>")

        fmt.Fprint(w, "<h1>Hello</h1>")
        //writeSVGGraph(w)
        fmt.Fprint(w, "</body></html>")
    })
    http.HandleFunc("/quit", func(w http.ResponseWriter, r *http.Request) {
        fmt.Fprint(w, "<body><h3>Shutting down!</h3></body>")
        time.AfterFunc(time.Second, func () {
            //client.Call("feproxy.Quit", struct{}{}, struct{}{})
            sigs <- os.Kill
        })
    })

    log.Print("Starting server...")
    // TODO(1.8): check for err == ErrServerClosed
    log.Fatal(http.ListenAndServe(":" + strconv.Itoa(int(lease.Port)), nil))
}
