package main

import (
    "testing"
    "reflect"
)

func TestUnion(t *testing.T) {
    cases := []struct{
        A []string
        B []string
        want []string
    }{
        {nil, nil, nil},
        {[]string{"1"}, nil,
         []string{"1"}},

        {nil, []string{"1"},
         []string{"1"}},

        {[]string{"1", "2", "3"}, []string{"2", "3", "4"},
         []string{"1", "2", "3", "4"}},

        {[]string{"1", "2", "3"}, []string{"4", "5", "6"},
         []string{"1", "2", "3", "4", "5", "6"}},

        {[]string{"4", "5", "6"}, []string{"1", "2", "3"},
         []string{"1", "2", "3", "4", "5", "6"}},

        {[]string{"1", "2", "4", "6"}, []string{"0", "2", "3", "4", "5"},
         []string{"0", "1", "2", "3", "4", "5", "6"}},

        {[]string{"1", "1", "1"}, []string{"1", "1", "2"},
         []string{"1", "2"}},

        {[]string{"0", "1", "1", "1", "5"}, []string{"1", "1", "2"},
         []string{"0", "1", "2", "5"}},
    }
    for _, c := range cases {
        got := union(c.A, c.B)
        if !reflect.DeepEqual(got, c.want) {
            t.Errorf("union(%v, %v) = %v; want %v",
                c.A, c.B, got, c.want)
        }
    }
}

func TestListDataDates(t *testing.T) {
    // TODO
}
