package main

import (
    "bufio"
    "errors"
    "io"
    "os"
    "os/signal"
    "fmt"
    "flag"
    "log"
    "strings"
    "sort"
    "path/filepath"
    "time"
    "math"
    "regexp"
    "net/http"
    "net/smtp"
    "strconv"

    "daemon/feproxy/proxyserv"
)

var (
    feproxyAddr = flag.String("feproxy_addr", "127.0.0.1:2048",
        "Address and port for the feproxy server")
    rootDataDir = flag.String("data_dir", "data",
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
    registerProd = "/climate/"
    registerTest = "/climate-test/"

    indoorDataDir = "indoor"
    outdoorDataDir = "outdoor"

    w1Dir = "/sys/bus/w1/devices/"
    w1MasterFilename = "w1_bus_master"
    w1SensorFilename = "w1_slave"
    // This is used for 12 bit output mode. I could check the whole message from
    // the sensor to check the mode, but I think the kernel always uses 12 bit.
    w1BitMultiplier = 0.0625

    alertDuplicateDelay = 15 * time.Minute
)

var (
    dateRegex = regexp.MustCompile(`\d{4}-\d{2}-\d{2}`)
    datePathRegex = regexp.MustCompile(`/?.*/(\d{4}-\d{2}-\d{2})?`)
)

func alertEmail(subject, message string) error {
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

func alert(message string, err error) {
    log.Printf("Alert! %v: %v", message, err)
    err = alertEmail(message, err.Error())
    if err != nil {
        log.Printf("Failed to send alert email: %v", err)
    }
}

var (
    lastAlert error
    lastAlertTime time.Time
)

func maybeAlert(message string, err error, now time.Time) {
    if lastAlert != nil && lastAlert.Error() == err.Error() &&
       now.Sub(lastAlertTime) < alertDuplicateDelay {
        log.Printf("(Repeat Alert) %v: %v", message, err)
    } else {
        alert(message, err)
    }
    lastAlert = err
    lastAlertTime = now
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

func logTemperature(sensor *os.File, dataDir string) {
    if err := os.MkdirAll(dataDir, os.FileMode(0775)); err != nil {
        // TODO: indoor / outdoor strings?
        log.Printf("Failed to make data directory %#v: %v", dataDir, err)
        return // TODO safe shutdown
    }
    var (
        dataFileDay int
        dataFile *os.File
        err error
    )
    for {
        now := time.Now()
        if dataFileDay != now.Day() { // Day is never 0, which is the default value
            dataFileDay = now.Day()
            currDateStr := fmt.Sprintf("%04d-%02d-%02d", now.Year(), now.Month(), now.Day())
            if dataFile != nil { // nil the first time
                err = dataFile.Close()
                if err != nil { // could be a write error on flush
                    alert("Failed to close data file", err)
                    // Still try to open the next file
                }
            }
            dataFile, err = os.OpenFile(filepath.Join(dataDir, currDateStr),
                os.O_RDWR|os.O_APPEND|os.O_CREATE, os.FileMode(0644))
            if err != nil {
                alert("Failed to open data log file for today", err)
                return
            }
        }
        temperature, err := readTemperature(sensor)
        if err != nil {
            maybeAlert("Error reading temperature", err, now)
        }
        dataFile.WriteString(fmt.Sprintf("%02d:%02d:%02d, %v\n",
            now.Hour(), now.Minute(), now.Second(), temperature))

        time.Sleep(*sensorReadInterval)
    }
    err = dataFile.Close()
    if err != nil {
        log.Printf("Failed to close final data file: %v", err)
    }
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
    // TODO: indoor/outdoor, need two lines in the svg
    const (
        width = 720
        height = 720

        topMargin = 25
        rightMargin = 55
        leftMargin = 55
        bottomMargin = 30
    )
    startOfDay, err := time.Parse("15:04:05", "00:00:00")
    if err != nil {
        log.Fatal("Failed to parse time constant:", err)
    }

    const (
        axisLineTmpl = "<line stroke-width=\"2\" stroke=\"black\" x1=\"%f\" y1=\"%f\" x2=\"%f\" y2=\"%f\"/>\n"
        tickLineTmpl = "<line stroke-width=\"1\" stroke=\"black\" x1=\"%f\" y1=\"%f\" x2=\"%f\" y2=\"%f\"/>\n"
        gridLineTmpl = "<line stroke-width=\"1\" stroke=\"%s\" x1=\"%f\" y1=\"%f\" x2=\"%f\" y2=\"%f\"/>\n"
        xAxisLabelTmpl = "<text fill=\"black\" font-size=\"16px\" dy=\"12\" text-anchor=\"middle\" x=\"%f\" y=\"%f\">%s</text>\n"
        yAxisLeftLabelTmpl = "<text fill=\"black\" font-size=\"16px\" dy=\"5\" text-anchor=\"end\" x=\"%f\" y=\"%f\">%s</text>\n"
        yAxisRightLabelTmpl = "<text fill=\"darkgrey\" font-size=\"16px\" dy=\"5\" text-anchor=\"start\" x=\"%f\" y=\"%f\">%s</text>\n"
    )

    fmt.Fprintf(w, "<svg viewport=\"0 0 %v %v\" preserveAspectRatio=\"xMinYMin\">\n",
        width, height)

    const (
        tempAxisPaddingCelsius = 1.0
        maxTempAxis = 35.0
        minTempAxis = 20.0
        numTempTicks = 16
        cToF = 9.0/5.0

        minutesBetweenTicks = 120

        tickLineLength = 8
        labelOffset = 10
    )
    //minTempAxis, maxTempAxis := findTempRange(data)
    //minTempAxis -= tempAxisPaddingCelsius
    //maxTempAxis += tempAxisPaddingCelsius

    gridLeft   := float32(leftMargin)
    gridRight  := float32(width - rightMargin)
    gridBottom := float32(height - bottomMargin)
    gridTop    := float32(topMargin)

    { // Axis labels
        const (
        )
        // Vertical
        for i := 0; i < numTempTicks; i++ {
            axisPosition := float32(i) / float32(numTempTicks - 1)
            temp := (axisPosition * (maxTempAxis - minTempAxis)) + minTempAxis
            yPos := ((1.0 - axisPosition) * (gridBottom - gridTop)) + gridTop

            fmt.Fprintf(w, yAxisLeftLabelTmpl,
                gridLeft - labelOffset, yPos,
                fmt.Sprintf("%.1f C", temp))
            fmt.Fprintf(w, yAxisRightLabelTmpl,
                gridRight + labelOffset, yPos,
                fmt.Sprintf("%.1f F", temp * (9.0 /5.0) + 32.0))
            fmt.Fprintf(w, tickLineTmpl,
                gridLeft, yPos,
                gridLeft - tickLineLength, yPos)
            fmt.Fprintf(w, gridLineTmpl, "lightgrey",
                gridLeft, yPos,
                gridRight, yPos)
        }
        // Horizonal
        const maxTimeMinutes = 60 * 24 - 1
        for timeMinutes := 0 ; true ; {
            timeStr := fmt.Sprintf("%02d:%02d", timeMinutes/60, timeMinutes%60)
            axisPosition := float32(timeMinutes) / float32(maxTimeMinutes)
            xPos := (axisPosition * (gridRight - gridLeft)) + gridLeft

            fmt.Fprintf(w, xAxisLabelTmpl,
                xPos, gridBottom + labelOffset,
                timeStr)
            fmt.Fprintf(w, tickLineTmpl,
                xPos, gridBottom,
                xPos, gridBottom + tickLineLength)
            fmt.Fprintf(w, gridLineTmpl, "darkgrey",
                xPos, gridTop,
                xPos, gridBottom)

            if timeMinutes != maxTimeMinutes {
                subTimeMinutes := timeMinutes + minutesBetweenTicks / 2
                subAxisPosition := float32(subTimeMinutes) / float32(maxTimeMinutes)
                subXPos := (subAxisPosition * (gridRight - gridLeft)) + gridLeft
                fmt.Fprintf(w, gridLineTmpl, "lightgrey",
                    subXPos, gridTop,
                    subXPos, gridBottom)
            }

            // Always make sure we have a tick at maxTimeMinutes
            if timeMinutes == maxTimeMinutes {
                break
            }
            timeMinutes += minutesBetweenTicks
            if timeMinutes > maxTimeMinutes {
                timeMinutes = maxTimeMinutes
            }
        }
    }
    { // Axes
        // Vertical
        fmt.Fprintf(w, axisLineTmpl,
            gridLeft, gridTop,
            gridLeft, gridBottom)
        // Horizonal
        fmt.Fprintf(w, axisLineTmpl,
            gridLeft, gridBottom,
            gridRight, gridBottom)
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
        plotPoint.X = (plotPoint.X * (gridRight - gridLeft)) + gridLeft
        plotPoint.Y = ((1.0 - plotPoint.Y) * (gridBottom - gridTop)) + gridTop
        line = append(line, plotPoint)
    }
    fmt.Fprintf(w, "%v", line)
    fmt.Fprintf(w, "</svg>")
}

// Returns a list of the data filenames (if the data dir is listable).
// All names are in the form yyyy-mm-dd and are sorted newest first.
func listDataFiles() ([]string, error) {
    // TODO: fix for indoor/outdoor, should find the max
    f, err := os.Open(filepath.Join(*rootDataDir, indoorDataDir))
    if err != nil {
        return nil, err
    }
    dataFiles, err := f.Readdir(-1)
    if err != nil {
        return nil, err
    }
    var ret []string
    for _, dataFile := range dataFiles {
        filename := dataFile.Name()
        if !dateRegex.MatchString(filename) {
            continue
        }
        ret = append(ret, filename)
    }
    // sort.Strings sorts oldest first :(
    sort.Slice(ret, func(i, j int) bool {
        return ret[i] > ret[j]
    })
    return ret, nil
}

// Finds the newest data file and the one immediately before it.
//
//   - If err != nil then the data directory was not listable.
//   - If there are no data files, then both prev and newest will be the empty
//     string.
//   - If there is only one data file, then prev will be the empty string.
func findNewestDataFile() (prev, newest string, err error) {
    // TODO: indoor/outdoor
    dates, err := listDataFiles()
    if err != nil {
        return
    }
    newest = dates[0]
    if len(dates) >= 2 {
        prev = dates[1]
    }
    return
}

// Finds the two data files immediately before and after the mid date.
//
//   - If there are no adjacent files in a direction, the empty string is
//     returned.
//   - If err != nil then the data directory was not listable.
//   - If mid does not exist then ("", "", nil) is returned.
func findAdjacentDataFiles(mid string) (prev, next string, err error) {
    // TODO: indoor/outdoor
    dates, err := listDataFiles()
    if err != nil {
        return
    }
    var lastDate string
    for _, date := range dates {
        if date == mid {
            next = lastDate
        } else if lastDate == mid {
            prev = date
            break
        }
        lastDate = date
    }
    return
}

func HandleGraphPage(w http.ResponseWriter, r *http.Request) {
    // TODO: indoor/outdoor
    w.Header().Set("Content-Type", "text/html")
    fmt.Fprint(w,
`<!doctype html>
<html>
<head>
<style>
h2 {
    text-align: center;
}
nav {
    width: 720px;
    margin: 0 auto;
    display: table;
    table-layout: fixed;
}
nav * {
    display: table-cell;
    vertical-align: middle;
    width: 0; /* equal widths with table-layout: fixed */
}
nav a#prev {
    text-align: left;
}
nav a#next {
    text-align: right;
}
svg {
    width: 720px;
    height: 720px;
    display: block;
    margin: 0 auto;
}
</style>
</head>
<body>`)
    defer fmt.Fprint(w, "\n</body></html>")

    // Parse the url for the date argument
    dateMatches := datePathRegex.FindStringSubmatch(r.URL.Path)
    if dateMatches == nil {
        w.WriteHeader(http.StatusNotFound)
        // Don't print the url here so unless you escape it
        fmt.Fprint(w, "<h2>Invalid URL</h2>")
        return
    }
    date := dateMatches[1] // First submatch
    // Find the file to display, and the prev and next for navigation
    var err error
    var prev, next string
    if date == "" {
        prev, date, err = findNewestDataFile()
    } else {
        prev, next, err = findAdjacentDataFiles(date)
    }
    if err != nil {
        w.WriteHeader(http.StatusInternalServerError)
        log.Printf("HTTP 500 Error; Invalid data directory: %v", err)
        fmt.Fprint(w, "<h2>Internal Server Error: Invalid data directory</h2>")
        return
    }
    if date == "" {
        w.WriteHeader(http.StatusNotFound)
        fmt.Fprint(w, "<h2>No data for %v</h2>", err)
        return
    }
    // Read the file to display
    filename := filepath.Join(*rootDataDir, date)
    data, err := readTempLog(filename)
    if err != nil {
        w.WriteHeader(http.StatusNotFound)
        fmt.Fprint(w, "<h2>No data for %v</h2>", err)
        return
    }
    // Output the navigation bar
    fmt.Fprintf(w, "<nav>")
    if prev != "" {
        fmt.Fprintf(w, `    <a id="prev" href="%s">%s</a>`, prev, prev)
    } else {
        fmt.Fprintf(w, "    <div></div>")
    }
    fmt.Fprintf(w, "    <h2>%s</h2>", date)
    if next != "" {
        fmt.Fprintf(w, `    <a id="next" href="%s">%s</a>`, next, next)
    } else {
        fmt.Fprintf(w, "    <div></div>")
    }
    fmt.Fprintf(w, "</nav>")

    plotTempSVG(data, w)
}

func promptForIntLessThan(max int) int {
    var ret int
    fmt.Scanln(&ret)
    for ret >= max {
        fmt.Println("Out of range, try again (-1 to stop): ")
        fmt.Scanln(&ret)
    }
    return ret
}

func promptForSensorFiles() (indoor, outdoor *os.File, err error) {
    // List the device files
    devicesDir, err := os.Open(w1Dir)
    if err != nil {
        return nil, nil, err
    }
    names, err := devicesDir.Readdirnames(-1)
    devicesDir.Close()
    if err != nil {
        return nil, nil, err
    }
    // Open each sensor file and read the temperature
    type Device struct{
        file *os.File
        temperature float32
    }
    var devices []Device
    for _, name := range names {
        if strings.HasPrefix(name, w1MasterFilename) {
            continue
        }
        sensorFilename := filepath.Join(w1Dir, name, w1SensorFilename)
        sensorFile, err := os.Open(sensorFilename)
        if err != nil {
            return nil, nil, err
        }
        temperature, err := readTemperature(sensorFile)
        if err != nil {
            return nil, nil, fmt.Errorf(
                "Failed to read sensor file %#v: %v", sensorFilename, err)
        }
        devices = append(devices, Device{
            file:        sensorFile,
            temperature: temperature,
        })
    }
    // Prompt the user for the indoor and outdoor sensor indexes
    for i, d := range devices {
        fmt.Printf("%v. temperature reading: %v C\n", i, d.temperature)
    }
    fmt.Printf("Enter index of indoor sensor: ")
    indoorIdx := promptForIntLessThan(len(devices))
    fmt.Printf("Enter index of outdoor sensor: ")
    outdoorIdx := promptForIntLessThan(len(devices))
    // Close any un-used opened files (the gc would do it, but I can't help it)
    for i, d := range devices {
        if i == indoorIdx || i == outdoorIdx {
            continue
        }
        d.file.Close()
    }
    if indoorIdx == -1 || outdoorIdx == -1 {
        return nil, nil, errors.New(
            "Failed to select indoor and outdoor sensor files")
    }
    return devices[indoorIdx].file, devices[outdoorIdx].file, nil
}

func main() {
    flag.Parse()
    sigs := make(chan os.Signal, 2)
    signal.Notify(sigs, os.Interrupt, os.Kill)

    register := registerProd
    if *testWebsiteOnly {
        register = registerTest
    }
    fe, lease := proxyserv.MustConnectAndRegister(*feproxyAddr, register)
    defer fe.Unregister(lease.Pattern)
    defer fe.Close()
    go fe.MustKeepLeaseRenewedForever(lease)

    go func() {
        <-sigs
        log.Print("Shutting down...")
        // TODO(1.8): use http.Server and call close here
        os.Exit(0)
    }()

    if !*testWebsiteOnly {
        indoorFile, outdoorFile, err := promptForSensorFiles()
        if err != nil {
            log.Fatal(err)
        }
        go logTemperature(indoorFile, filepath.Join(*rootDataDir, indoorDataDir))
        go logTemperature(outdoorFile, filepath.Join(*rootDataDir, outdoorDataDir))
    }

    http.HandleFunc(register, HandleGraphPage)
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
