package main

import (
    "testing"
)

func TestPathRegex(t *testing.T) {
    cases := []struct{
        input string
        wantMatch bool
        wantFolder string
        wantDate string
    }{
        {"climate", true, "", ""},
        {"climate/", true, "", ""},
        {"/climate/", true, "", ""},
        {"/climate/2018-10-02", true, "", "2018-10-02"},
        {"/climate/2018-10-02/", true, "", "2018-10-02"},
        {"/climate/indoor/", true, "indoor", ""},
        {"/climate/outdoor", true, "outdoor", ""},
        {"/climate/indoor/2099-99-99", true, "indoor", "2099-99-99"},
        {"/climate/outdoor/2018-01-01/", true, "outdoor", "2018-01-01"},

        {"/climate/outdoor/something/", false, "", ""},
        {"/climate/indoor/6464", false, "", ""},
        {"/climate/indoor/2018-12-12/extra", false, "", ""},
    }
    for _, c := range cases {
        matches := pathRegex.FindStringSubmatch(c.input)
        if c.wantMatch != (matches != nil) {
            t.Errorf("pathRegex(%#v): matched = %v; want matched = %v",
                c.input, (matches != nil), c.wantMatch)
            continue
        }
        if matches == nil {
            continue
        }
        if c.wantFolder != matches[1] || c.wantDate != matches[2] {
            t.Errorf("pathRegex(%#v) = (%#v, %#v); want (%#v, %#v)",
                c.input, matches[1], matches[2], c.wantFolder, c.wantDate)
        }
    }
    if t.Failed() {
        t.Logf("pathRegex = %v", pathRegex)
    }
}
