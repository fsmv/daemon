package main

import (
    "io"
    "fmt"
    "log"
    "time"
    "regexp"
    "net/http"
    "path/filepath"
)

var (
    pathRegex = regexp.MustCompile(
        // Note: (?:re) means non-capturing group
        `^/?[A-Za-z_-]+`+            // The registered path for this app
        `(?:/([A-Za-z_-]+))?`+       // The optional data folder
        `(?:/(\d{4}-\d{2}-\d{2}))?`+ // the optional data date
        `/?$`)                       // Nothing extra on the end
)

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

func HandleGraphPage(w http.ResponseWriter, r *http.Request) {
    // TODO: indoor/outdoor
    w.Header().Set("Content-Type", "text/html")
    head :=
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
<body>
`
    defer fmt.Fprint(w, "\n</body></html>")

    // Parse the url for the date and folder arguments
    pathMatches := pathRegex.FindStringSubmatch(r.URL.Path)
    if pathMatches == nil {
        w.WriteHeader(http.StatusNotFound)
        // Don't print the url here so unless you escape it
        fmt.Fprintf(w, "%v<h2>Invalid URL</h2>", head)
        return
    }
    optionalDataSubdir := pathMatches[1]
    date := pathMatches[2]
    // Find the date to display, and the prev and next dates for navigation
    folder := filepath.Join(*rootDataDir, optionalDataSubdir)
    var err error
    var prev, next string
    if date == "" {
        prev, date, err = findNewestDataDate(folder)
    } else {
        prev, next, err = findAdjacentDataDates(folder, date)
    }
    if err != nil {
        w.WriteHeader(http.StatusInternalServerError)
        log.Printf("HTTP 500 Error; Invalid data directory: %v", err)
        fmt.Fprintf(w, "%v<h2>Internal Server Error: Invalid data directory</h2>", head)
        return
    }
    if date == "" {
        w.WriteHeader(http.StatusNotFound)
        fmt.Fprintf(w, "%v<h2>No data found</h2>", head)
        return
    }
    // Read the file to display
    filename := filepath.Join(folder, date)
    data, err := readTempLog(filename)
    if err != nil {
        w.WriteHeader(http.StatusNotFound)
        log.Printf("HTTP 404 Error; Failed to read data file (%v): %v",
            filename, err)
        fmt.Fprintf(w, "%v<h2>No data for %v</h2>", head, date)
        return
    }
    // No errors, print the header section (200 OK)
    fmt.Fprint(w, head)
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
