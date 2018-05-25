package main

import (
    "bufio"
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
    register = "/climate-test"

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
        log.Printf("Failed to open sensor file: %v", err)
        return
    }
    if err := os.MkdirAll(*dataDir, os.FileMode(0775)); err != nil {
        log.Printf("Failed to make data directory %#v: %v", *dataDir, err)
        return
    }
    var once bool
    var currDay int
    var currFile *os.File
    for {
        now := time.Now()
        currTemp, err := readTemperature(sensorFile)
        if err != nil {
            log.Printf("Failed to read temperature: %v", err)
        }
        if currDay != now.Day() {
            currDay = now.Day()
            currDate := fmt.Sprintf("%04d-%02d-%02d", now.Year(), now.Month(), now.Day())
            currFile.Close()
            currFile, err = os.OpenFile(filepath.Join(*dataDir, currDate),
                os.O_RDWR|os.O_APPEND|os.O_CREATE, os.FileMode(0644))
            if err != nil {
                log.Printf("Failed to open data log file for today: %v", err)
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
    X, Y float32
}

type SVGPolyline []Point

func (l SVGPolyline) Format(f fmt.State, c rune) {
    if len(l) < 2 {
        fmt.Fprint(f, "<!-- error, not enough points -->\n")
        return
    }
    firstPoint := l[0]
    fmt.Fprintf(f, "<polyline fill=\"none\" stroke=\"black\" points=\"%f %f",
        firstPoint.X, firstPoint.Y)
    for _, point := range l[1:] {
        fmt.Fprintf(f, ", %f %f", point.X, point.Y)
    }
    fmt.Fprint(f, "\"/>\n")
}

type TemperatureReading struct {
    Time    time.Time
    Celsius float32
}

func parseTempLine(line string) (TemperatureReading, error) {
    var ret TemperatureReading
    var timeStr, tempStr string
    for i := 0; i < len(line); i++ {
        if line[i] != ',' {
            if i == len(line)-1 {
                return ret, fmt.Errorf("expected comma")
            }
            continue
        }
        timeStr = line[:i]
        tempStr = line[i+2:]
        break
    }
    var err error
    ret.Time, err = time.Parse("15:04:05", timeStr)
    if err != nil {
        return ret, fmt.Errorf("failed to parse time: %v", err)
    }
    temp64, err := strconv.ParseFloat(tempStr, 32)
    ret.Celsius = float32(temp64)
    if err != nil {
        return ret, fmt.Errorf("failed to parse temperature: %v", err)
    }
    return ret, nil
}

func readTempLog(filename string) ([]TemperatureReading, error) {
    f, err := os.Open(filename)
    if err != nil {
        return nil, err
    }
    defer f.Close()
    sc := bufio.NewScanner(f)
    var ret []TemperatureReading
    for sc.Scan() {
        temp, err := parseTempLine(sc.Text())
        if err != nil {
            return nil, err
        }
        ret = append(ret, temp)
    }
    if err := sc.Err(); err != nil {
        return nil, err
    }
    return ret, nil
}

func plotTempSVG(data []TemperatureReading, w io.Writer) {
    const (
        width = 1280
        height = 720
        outerPadding = 0.04
        tickLength = 0.01
    )

    const line = "<line stroke-width=\"0.002\" stroke=\"black\" x1=\"%f\" y1=\"%f\" x2=\"%f\" y2=\"%f\"/>\n"
    const text = "<text x=\"%f\" y=\"%f\">%s</text>"

    fmt.Fprintf(w, "<svg width=\"1280\" height=\"720\" viewbox=\"0 0 1 1\" preserveAspectRatio=\"xMinYMin\">\n")
    { // Axes
        // Vertical
        fmt.Fprintf(w, line, outerPadding, outerPadding, outerPadding, 1.0 - outerPadding)

        // Horizonal
        fmt.Fprintf(w, line, outerPadding, 1.0 - outerPadding, 1.0 - outerPadding, 1.0 - outerPadding)
    }
    fmt.Printf("First temp reading: %v\n", data[0])
    //fmt.Fprintf(w, "%v\n", SVGPolyline(Data))
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
    log.Printf("Obtained lease for %#v, port: %v, ttl: %v",
        register, lease.Port, lease.TTL)

    defer client.Call("feproxy.Deregister", register, &lease)
    go func() {
        <-sigs
        //client.Call("feproxy.Deregister", register, &lease)
        log.Print("Shutting down...")
        // TODO(1.8): use http.Server and call close here
        os.Exit(0)
    }()

    //go logTemperature()

    http.HandleFunc(register, func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Content-Type", "text/html")
        fmt.Fprint(w, "<!doctype html>\n<html><body>\n")
        defer fmt.Fprint(w, "\n</body></html>")

        const filename = "data/2018-05-21"
        data, err := readTempLog(filename)
        if err != nil {
            fmt.Fprint(w, "<h2>Error reading file: %v</h2>", err)
            return
        }
        //fmt.Fprint(w, "<h1>Hello</h1>\n")
        plotTempSVG(data, w)
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
