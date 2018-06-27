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
    "math"
    "net/http"
    "net/rpc"
    "net/smtp"
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

    alertEmailAddr = flag.String("alert_email_addr", "",
        "Email address to send alerts to (from itself)")
    alertEmailPassword = flag.String("alert_email_password", "",
        "SMTP password for the alert email address")
    alertServerAddr = flag.String("alert_server_addr", "",
        "SMTP server address for sending alerts")

    testWebsiteOnly = flag.Bool("test_website_only", false,
        "If true, register under /climate-test and don't run the temperature logging code.")
)

const (
    registerProd = "/climate"
    registerTest = "/climate-test"

    w1Dir = "/sys/bus/w1/devices/"
    w1MasterFilename = "w1_bus_master"
    w1SensorFilename = "w1_slave"
    // This is used for 12 bit output mode. I could check the whole message from
    // the sensor to check the mode, but I think the kernel always uses 12 bit.
    w1BitMultiplier = 0.0625

    alertDuplicateDelay = 15 * time.Minute
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

// One degree (0.85 actually) below absolute zero
const TemperatureErrVal = -274.0

// Returns the temperature read from the w1 file in Celsius
// On error, returns TemperatureErrVal
func readTemperature(sensorFile *os.File) (float32, error) {
    // Read the data
    sensorFile.Sync()
    data := make([]byte, 5)
    _, err := sensorFile.ReadAt(data, /*offset=*/0)
    if err != nil {
        return TemperatureErrVal,
            fmt.Errorf("Failed to read file: %v", err)
    }
    // Parse the data
    if data[2] != ' ' {
        return TemperatureErrVal,
            fmt.Errorf("Invalid file format: %#v", string(data))
    }
    hexStr := string(append(data[3:5], data[0], data[1]))
    reading, err := strconv.ParseInt(hexStr, /*base=*/16, /*bits=*/16)
    if err != nil {
        return TemperatureErrVal,
            fmt.Errorf("Invalid file format: %#v", string(data))
    }
    return float32(reading) * w1BitMultiplier, nil;
}

func alert(subject, message string) error {
    log.Printf("Alert! %v: %v", subject, message)
    if *alertServerAddr == "" || *alertEmailAddr == "" || *alertEmailPassword == "" {
        log.Print("Alert emails disabled (the flags must be set)")
        return nil
    }
    headers := fmt.Sprintf("From: %v\nTo: %v\nSubject: %v\n\n",
        *alertEmailAddr, *alertEmailAddr, subject)
    // Strip the port, might only work with gmail
    authHost := (*alertServerAddr)[:strings.Index(*alertServerAddr, ":")]
    return smtp.SendMail(*alertServerAddr,
        smtp.PlainAuth("", *alertEmailAddr, *alertEmailPassword, authHost),
        *alertEmailAddr, []string{*alertEmailAddr}, []byte(headers + message))
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
    var (
        once bool

        currDay int
        currFile *os.File

        lastAlert error
        lastAlertTime time.Time
    )
    for {
        now := time.Now()
        currTemp, err := readTemperature(sensorFile)
        if err != nil {
            now = time.Now()
            if lastAlert != nil && lastAlert.Error() == err.Error() &&
               now.Sub(lastAlertTime) < alertDuplicateDelay {
                log.Printf("(Repeat Alert) Error reading temperature: %v", err)
            } else {
                alert("Error reading temperature", err.Error())
            }
            lastAlert = err
            lastAlertTime = now
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
    // TODO: make stroke-width configurable
    fmt.Fprintf(f, "<polyline fill=\"none\" stroke-width=\"1\" stroke=\"black\" points=\"%f %f",
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

func findTempRange(data []TemperatureReading) (min float32, max float32) {
    min, max = math.MaxFloat32, -math.MaxFloat32
    for _, r := range data {
        if r.Celsius < min {
            min = r.Celsius
        }
        if r.Celsius > max {
            max = r.Celsius
        }
    }
    return
}

func plotTempSVG(data []TemperatureReading, w io.Writer) {
    const (
        width = 720
        height = 720
        outerPadding = 45.0
        tickLength = 5.0

        tempAxisPaddingCelsius = 1.0
    )
    startOfDay, err := time.Parse("15:04:05", "00:00:00")
    if err != nil {
        log.Fatal("Failed to parse time constant:", err)
    }

    const lineTmpl = "<line stroke-width=\"2\" stroke=\"black\" x1=\"%f\" y1=\"%f\" x2=\"%f\" y2=\"%f\"/>\n"
    const textTmpl = "<text font-size=\"12px\" text-anchor=\"middle\" x=\"%f\" y=\"%f\">%s</text>\n"

    fmt.Fprintf(w, "<svg width=\"%v\" height=\"%v\" viewport=\"0 0 %v %v\" preserveAspectRatio=\"xMinYMin\">\n",
        width, height, width, height)
    { // Axes
        // Vertical
        fmt.Fprintf(w, lineTmpl,
            outerPadding, outerPadding,
            outerPadding, height - outerPadding)

        // Horizonal
        fmt.Fprintf(w, lineTmpl,
            outerPadding, height - outerPadding,
            width - outerPadding, height - outerPadding)
    }

    minTempAxis, maxTempAxis := findTempRange(data)
    minTempAxis -= tempAxisPaddingCelsius
    maxTempAxis += tempAxisPaddingCelsius

    { // Axis labels
        const (
            textYAxisOffset = 15.0
            textXAxisOffset = 25.0
        )
        // Vertical
        fmt.Fprintf(w, textTmpl, outerPadding - textXAxisOffset, height - outerPadding,
            fmt.Sprintf("%.2f C", minTempAxis))
        fmt.Fprintf(w, textTmpl, outerPadding - textXAxisOffset, outerPadding,
            fmt.Sprintf("%.2f C", maxTempAxis))
        // Horizonal
        fmt.Fprintf(w, textTmpl, outerPadding, height - outerPadding + textYAxisOffset,
            "00:00")
        fmt.Fprintf(w, textTmpl, width - outerPadding, height - outerPadding + textYAxisOffset,
            "23:59")
    }

    var line SVGPolyline
    for _, t := range data {
        durationIntoDay := t.Time.Sub(startOfDay)
        percentIntoDay := durationIntoDay.Seconds() / (24 * time.Hour).Seconds()
        // The data where X and Y are between 0 and 1
        plotPoint := Point{
            X: float32(percentIntoDay),
            Y: (t.Celsius - minTempAxis) / (maxTempAxis - minTempAxis),
        }
        // Add the padding for the margin before the axes
        plotPoint.X = (plotPoint.X * (width - 2*outerPadding)) + outerPadding
        plotPoint.Y = ((1.0 - plotPoint.Y) * (height - 2*outerPadding)) + outerPadding
        line = append(line, plotPoint)
    }
    fmt.Fprintf(w, "%v\n", line)
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

    register := registerProd
    if *testWebsiteOnly {
        register = registerTest
    }
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

    if !*testWebsiteOnly {
        go logTemperature()
    }

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
