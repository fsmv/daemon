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

// color string must be sanitized for output to HTML
func plotPolylineSVG(w io.Writer, l []Point, color string, strokeWidth int) {
    if len(l) < 2 {
        fmt.Fprint(w, "<!-- error, not enough points -->\n")
        return
    }
    firstPoint := l[0]
    fmt.Fprintf(w, "<polyline fill=\"none\" stroke=\"%v\" stroke-width=\"%v\" points=\"%f %f",
        color, strokeWidth, firstPoint.X, firstPoint.Y)
    for _, point := range l[1:] {
        fmt.Fprintf(w, ", %f %f", point.X, point.Y)
    }
    fmt.Fprint(w, "\"/>\n")
}

type TemperatureLine struct {
    Folder string
    Data []TemperatureReading
}

func plotTemperatureGraphSVG(w io.Writer, data []TemperatureLine) {
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
        lineTmpl = "<line stroke-width=\"%d\" stroke=\"%s\" x1=\"%f\" y1=\"%f\" x2=\"%f\" y2=\"%f\"/>\n"
        xAxisLabelTmpl = "<text fill=\"black\" font-size=\"16px\" dy=\"12\" text-anchor=\"middle\" x=\"%f\" y=\"%f\">%s</text>\n"
        yAxisLeftLabelTmpl = "<text fill=\"black\" font-size=\"16px\" dy=\"5\" text-anchor=\"end\" x=\"%f\" y=\"%f\">%s</text>\n"
        yAxisRightLabelTmpl = "<text fill=\"darkgrey\" font-size=\"16px\" dy=\"5\" text-anchor=\"start\" x=\"%f\" y=\"%f\">%s</text>\n"
        legendLabelTmpl = "<text fill=\"black\" font-size=\"16px\" dy=\"7\" text-anchor=\"start\" x=\"%f\" y=\"%f\">%s</text>\n"
        rectTmpl = "<rect fill=\"white\" stroke=\"black\" x=\"%f\" y=\"%f\" width=\"%f\" height=\"%f\" />\n"
    )

    fmt.Fprintf(w, "<svg viewport=\"0 0 %v %v\" preserveAspectRatio=\"xMinYMin\">\n",
        width, height)

    const (
        tempAxisPaddingCelsius = 1.0
        maxTempAxis = 30.0
        minTempAxis = 6.0
        numTempTicks = 13
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

    { // Plot the axes
        // Vertical ticks and labels
        for i := 0; i < numTempTicks; i++ {
            axisPosition := float32(i) / float32(numTempTicks - 1)
            temp := (axisPosition * (maxTempAxis - minTempAxis)) + minTempAxis
            yPos := ((1.0 - axisPosition) * (gridBottom - gridTop)) + gridTop

            fmt.Fprintf(w, yAxisLeftLabelTmpl, // left axis label
                gridLeft - labelOffset, yPos,
                fmt.Sprintf("%.1f C", temp))
            fmt.Fprintf(w, yAxisRightLabelTmpl, // right axis label
                gridRight + labelOffset, yPos,
                fmt.Sprintf("%.1f F", temp * (9.0 /5.0) + 32.0))
            fmt.Fprintf(w, lineTmpl, 1, "black", // left axis tick
                gridLeft, yPos,
                gridLeft - tickLineLength, yPos)
            fmt.Fprintf(w, lineTmpl, 1, "darkgrey", // grid line
                gridLeft, yPos,
                gridRight, yPos)
        }
        fmt.Fprintf(w, lineTmpl, 2, "black", // Vertical axis line
            gridLeft, gridTop,
            gridLeft, gridBottom)
        // Horizonal axis ticks and labels
        const maxTimeMinutes = 60 * 24 - 1
        for timeMinutes := 0 ; true ; {
            timeStr := fmt.Sprintf("%02d:%02d", timeMinutes/60, timeMinutes%60)
            axisPosition := float32(timeMinutes) / float32(maxTimeMinutes)
            xPos := (axisPosition * (gridRight - gridLeft)) + gridLeft

            fmt.Fprintf(w, xAxisLabelTmpl, // tick label
                xPos, gridBottom + labelOffset,
                timeStr)
            fmt.Fprintf(w, lineTmpl, 1, "black", // tick line
                xPos, gridBottom,
                xPos, gridBottom + tickLineLength)
            fmt.Fprintf(w, lineTmpl, 1, "darkgrey", // grid line
                xPos, gridTop,
                xPos, gridBottom)

            if timeMinutes != maxTimeMinutes {
                subTimeMinutes := timeMinutes + minutesBetweenTicks / 2
                subAxisPosition := float32(subTimeMinutes) / float32(maxTimeMinutes)
                subXPos := (subAxisPosition * (gridRight - gridLeft)) + gridLeft
                fmt.Fprintf(w, lineTmpl, 1, "lightgrey", // minor grid line
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
        fmt.Fprintf(w, lineTmpl, 2, "black", // Horizontal axis line
            gridLeft, gridBottom,
            gridRight, gridBottom)
    }
    { // Plot the data lines + legend more than one line
        tempsToPoints := func (temps []TemperatureReading) []Point {
            var ret []Point
            for _, t := range temps {
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
                ret = append(ret, plotPoint)
            }
            return ret
        }
        if len(data) == 1 { // Only one line, no legend
            plotPolylineSVG(w, tempsToPoints(data[0].Data), "black", 1)
            fmt.Fprintf(w, "</svg>")
            return
        }
        colors := []string{"blue", "red", "green", "orange", "magenta", "black"}
        getColor := func(index int) string {
            if index >= len(colors) {
                return colors[len(colors)-1]
            }
            return colors[index]
        }
        // Plot the data lines
        for i, line := range data {
            plotPolylineSVG(w, tempsToPoints(line.Data), getColor(i), 1)
        }
        // Plot the legend
        const (
            legendMargin = 5.0
            legendPadding = 5.0
            legendLineSpacing = 5.0
            legendTickLength = 15.0
            legendCharWidth = 12.0
            legendCharHeight = 16.0
        )
        legendHeight := float32(len(data)) * (legendLineSpacing + legendCharHeight) + legendPadding
        var legendTextWidth float32
        for _, line := range data {
            folderWidth := float32(len(line.Folder) * legendCharWidth + legendPadding) 
            if folderWidth > legendTextWidth {
                legendTextWidth = folderWidth
            }
        }
        legendWidth := legendTextWidth + legendTickLength + 2*legendPadding
        legendLeft := gridRight - legendWidth - legendMargin
        legendTop := gridTop + legendMargin
        fmt.Fprintf(w, rectTmpl,
            legendLeft, legendTop, legendWidth, legendHeight)
        for i, line := range data {
            lineY := legendTop + legendPadding + legendLineSpacing + float32(i) * (legendCharHeight + legendLineSpacing)
            fmt.Fprintf(w, legendLabelTmpl, // legend label
                legendLeft + legendPadding, lineY,
                line.Folder)
            legendTickLeft := legendLeft + legendTextWidth
            fmt.Fprintf(w, lineTmpl, 1, getColor(i), // legend example line (tick)
                legendTickLeft, lineY,
                legendTickLeft + legendTickLength, lineY)
        }
    }
}

type GraphPageHandler struct {
    RootDataDir string
}

func (h *GraphPageHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    w.Header().Set("Content-Type", "text/html")
    head :=
`<!doctype html>
<html>
<head>
<link href="https://fonts.googleapis.com/css?family=Roboto" rel="stylesheet">
<style>
* {
    font-family: 'Roboto', sans-serif;
}
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
    optionalDataFolder := pathMatches[1]
    optionalDataDate := pathMatches[2]
    // Find the date to display, the folders that have data for that date, and
    // the prev and next dates for navigation
    date := optionalDataDate
    var folders []string
    var prev, next string
    var err error
    if date == "" {
        prev, date, folders, err = FindNewestDataDate(h.RootDataDir)
    } else {
        prev, next, folders, err = FindAdjacentDataDates(h.RootDataDir, date)
    }
    if err != nil {
        w.WriteHeader(http.StatusInternalServerError)
        log.Printf("HTTP 500 Error; Invalid data directory: %v", err)
        fmt.Fprintf(w, "%v<h2>Internal Server Error: Invalid data directory</h2>", head)
        return
    }
    if date == "" || len(folders) == 0 {
        w.WriteHeader(http.StatusNotFound)
        fmt.Fprintf(w, "%v<h2>No data found</h2>", head)
        return
    }
    if optionalDataFolder != "" {
        var optFolderHasData bool
        for _, folder := range folders {
            if folder != optionalDataFolder {
                continue
            }
            optFolderHasData = true
        }
        if !optFolderHasData {
            w.WriteHeader(http.StatusNotFound)
            fmt.Fprintf(w, "%v<h2>No data found for specfied folder</h2>", head)
            return
        }
    }

    // After this point, date and optionalDataFolder are known safe strings even
    // though they're user input.
    //  - date must have matched the date regex and be a file on the disk
    //  - optionalDataFolder must match the name of a data folder on disk

    // Read the file(s) to display
    var lines []TemperatureLine
    for _, folder := range folders {
        if optionalDataFolder != "" && optionalDataFolder != folder {
            continue // Only output the one folder if specified
        }
        filename := filepath.Join(h.RootDataDir, folder, date)
        data, err := readTempLog(filename)
        if err != nil {
            log.Printf("Failed to read data file (%v): %v",
                filename, err)
            continue
        }
        lines = append(lines, TemperatureLine{
            Folder: folder,
            Data: data,
        })
    }
    if len(lines) == 0 {
        w.WriteHeader(http.StatusNotFound)
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
    if optionalDataFolder == "" {
        fmt.Fprintf(w, "    <h2>%s</h2>", date)
    }else {
        fmt.Fprintf(w, "    <h2>%s/%s</h2>", optionalDataFolder, date)
    }
    if next != "" {
        fmt.Fprintf(w, `    <a id="next" href="%s">%s</a>`, next, next)
    } else {
        fmt.Fprintf(w, "    <div></div>")
    }
    fmt.Fprintf(w, "</nav>")

    plotTemperatureGraphSVG(w, lines)
}
