package main

import (
    "fmt"
    "log"
    "strings"
    "time"
    "net/smtp"
    "flag"
)

var (
    alertEmailAddr = flag.String("alert_email_addr", "",
        "Email address to send alerts to (from itself)")
    alertEmailPassword = flag.String("alert_email_password", "",
        "SMTP password for the alert email address")
    alertServerAddr = flag.String("alert_server_addr", "",
        "SMTP server address for sending alerts")


    lastAlert error
    lastAlertTime time.Time
    alertDuplicateDelay = 15 * time.Minute
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
